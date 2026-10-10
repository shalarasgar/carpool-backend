package config

import (
	"context"
	"fmt"
	"os"

	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/option"
)

// firebaseCredentialsFile is the Firebase service account key in the project root.
const firebaseCredentialsFile = "service-account.json"

// InitFirebase starts the Firebase Admin app from the project-root service account.
func InitFirebase(ctx context.Context) (*firebase.App, error) {
	if _, err := os.Stat(firebaseCredentialsFile); err != nil {
		return nil, fmt.Errorf("firebase credentials: %w", err)
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{}, option.WithCredentialsFile(firebaseCredentialsFile))
	if err != nil {
		return nil, fmt.Errorf("init firebase: %w", err)
	}
	return app, nil
}
