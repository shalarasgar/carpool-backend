// Package push delivers inbox notifications to devices through Firebase Cloud Messaging.
package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	fcmScope        = "https://www.googleapis.com/auth/firebase.messaging"
	defaultTokenURL = "https://oauth2.googleapis.com/token"
	fcmSendURL      = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
	// AndroidChannel is the notification channel the app creates at start.
	AndroidChannel = "yolugra_default"
)

// ErrInvalidToken means FCM will never deliver to this token again; delete it.
var ErrInvalidToken = errors.New("push token is no longer valid")

// Message is one push: the visible text and the data the app uses to open a screen.
type Message struct {
	Title string
	Body  string
	Data  map[string]string
}

// FCM sends messages with the FCM HTTP v1 API, authenticated as a service account.
type FCM struct {
	projectID string
	email     string
	key       *rsa.PrivateKey
	tokenURL  string
	sendURL   string
	client    *http.Client
	now       func() time.Time

	mu     sync.Mutex
	access string
	expiry time.Time
}

type serviceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// LoadFCM reads a service account key file (Firebase console → Project settings →
// Service accounts → Generate new private key).
func LoadFCM(path string) (*FCM, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read FCM credentials: %w", err)
	}
	return NewFCM(data, nil)
}

// NewFCM parses service account JSON. A nil client uses a client with a 10 second timeout.
func NewFCM(credentials []byte, client *http.Client) (*FCM, error) {
	var sa serviceAccount
	if err := json.Unmarshal(credentials, &sa); err != nil {
		return nil, fmt.Errorf("parse FCM credentials: %w", err)
	}
	if sa.Type != "service_account" || sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("FCM credentials must be a service account key with project_id, client_email and private_key")
	}
	key, err := parseRSAKey(sa.PrivateKey)
	if err != nil {
		return nil, err
	}
	if sa.TokenURI == "" {
		sa.TokenURI = defaultTokenURL
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &FCM{
		projectID: sa.ProjectID,
		email:     sa.ClientEmail,
		key:       key,
		tokenURL:  sa.TokenURI,
		sendURL:   fmt.Sprintf(fcmSendURL, url.PathEscape(sa.ProjectID)),
		client:    client,
		now:       time.Now,
	}, nil
}

// ProjectID is the Firebase project the credentials belong to.
func (f *FCM) ProjectID() string { return f.projectID }

func parseRSAKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("FCM private_key is not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("FCM private_key is not an RSA key")
	}
	k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse FCM private_key: %w", err)
	}
	return k, nil
}

type fcmRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification fcmNotification   `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
	Android      fcmAndroid        `json:"android"`
	APNS         fcmAPNS           `json:"apns"`
}

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type fcmAndroid struct {
	Priority     string                 `json:"priority"`
	Notification fcmAndroidNotification `json:"notification"`
}

type fcmAndroidNotification struct {
	ChannelID    string `json:"channel_id"`
	DefaultSound bool   `json:"default_sound"`
}

type fcmAPNS struct {
	Payload struct {
		APS struct {
			Sound string `json:"sound"`
		} `json:"aps"`
	} `json:"payload"`
}

type fcmError struct {
	Error struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
		Details []struct {
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

// Send delivers m to one device token. It returns ErrInvalidToken when the token is
// unregistered or belongs to another Firebase project.
func (f *FCM) Send(ctx context.Context, token string, m Message) error {
	access, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	req := fcmRequest{Message: fcmMessage{
		Token:        token,
		Notification: fcmNotification{Title: m.Title, Body: m.Body},
		Data:         m.Data,
		Android: fcmAndroid{Priority: "HIGH", Notification: fcmAndroidNotification{
			ChannelID: AndroidChannel, DefaultSound: true,
		}},
	}}
	req.Message.APNS.Payload.APS.Sound = "default"
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode push: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, f.sendURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("push request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+access)
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := f.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send push: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode == http.StatusOK {
		return nil
	}
	if res.StatusCode == http.StatusUnauthorized {
		f.mu.Lock()
		f.access = ""
		f.mu.Unlock()
	}
	var fe fcmError
	_ = json.Unmarshal(raw, &fe)
	code := ""
	for _, d := range fe.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
			break
		}
	}
	if code == "UNREGISTERED" || code == "SENDER_ID_MISMATCH" || res.StatusCode == http.StatusNotFound {
		return ErrInvalidToken
	}
	return fmt.Errorf("send push: http %d %s %s", res.StatusCode, fe.Error.Status, code)
}

// accessToken returns a cached OAuth access token, refreshing it a minute before expiry.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.access != "" && now.Before(f.expiry.Add(-time.Minute)) {
		return f.access, nil
	}
	assertion, err := f.assertion(now)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch FCM access token: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch FCM access token: http %d", res.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || tok.AccessToken == "" {
		return "", errors.New("fetch FCM access token: malformed response")
	}
	f.access = tok.AccessToken
	f.expiry = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	return f.access, nil
}

// assertion is the signed JWT that a service account exchanges for an access token.
func (f *FCM) assertion(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss":   f.email,
		"scope": fcmScope,
		"aud":   f.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("encode JWT claims: %w", err)
	}
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
