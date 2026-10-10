// Package i18n resolves the request locale and localizes API messages.
package i18n

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Supported locales. DefaultLocale is used when Accept-Language is missing or unknown.
const (
	LocaleTK      = "tk"
	LocaleRU      = "ru"
	LocaleEN      = "en"
	DefaultLocale = LocaleTK
)

type contextKey int

const localeKey contextKey = 1

// Supported reports whether locale is one of tk, ru, en.
func Supported(locale string) bool {
	switch locale {
	case LocaleTK, LocaleRU, LocaleEN:
		return true
	}
	return false
}

// Resolve picks the best supported locale from an Accept-Language header value.
// Entries are weighted by their q parameter; ties keep header order.
func Resolve(acceptLanguage string) string {
	best := DefaultLocale
	bestQ := -1.0
	for _, part := range strings.Split(acceptLanguage, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		if tag == "" {
			continue
		}
		q := 1.0
		for _, param := range fields[1:] {
			param = strings.TrimSpace(param)
			if strings.HasPrefix(param, "q=") {
				if parsed, err := strconv.ParseFloat(param[2:], 64); err == nil {
					q = parsed
				}
			}
		}
		if q <= 0 {
			continue
		}
		lang := tag
		if i := strings.IndexAny(lang, "-_"); i > 0 {
			lang = lang[:i]
		}
		if !Supported(lang) {
			continue
		}
		if q > bestQ {
			best, bestQ = lang, q
		}
	}
	return best
}

// WithLocale stores a locale on the context.
func WithLocale(ctx context.Context, locale string) context.Context {
	if !Supported(locale) {
		locale = DefaultLocale
	}
	return context.WithValue(ctx, localeKey, locale)
}

// FromContext returns the locale stored by WithLocale, or DefaultLocale.
func FromContext(ctx context.Context) string {
	if locale, ok := ctx.Value(localeKey).(string); ok && Supported(locale) {
		return locale
	}
	return DefaultLocale
}

// ErrorCodes returns the stable machine-readable error codes, sorted.
func ErrorCodes() []string {
	codes := make([]string, 0, len(messages))
	for code := range messages {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// Message returns the localized text for an error code. Unknown codes fall
// back to the English text, then to the code itself so no response is empty.
func Message(locale, code string) string {
	if !Supported(locale) {
		locale = DefaultLocale
	}
	if m, ok := messages[code]; ok {
		if text := m[locale]; text != "" {
			return text
		}
		if text := m[LocaleEN]; text != "" {
			return text
		}
	}
	return code
}

// Notification returns the inbox title and body for a stored event type.
// The text is chosen from the recipient's saved locale at read time.
func Notification(locale, kind string) (title, body string) {
	if !Supported(locale) {
		locale = DefaultLocale
	}
	copy, ok := notificationCopy[kind]
	if !ok {
		return kind, ""
	}
	text, ok := copy[locale]
	if !ok {
		text = copy[DefaultLocale]
	}
	return text.title, text.body
}

type notificationText struct {
	title string
	body  string
}

var notificationCopy = map[string]map[string]notificationText{
	"booking_requested": {
		LocaleTK: {title: "Täze ýer haýyşy", body: "Ýolagçy syýahatyňyza ýer sorady."},
		LocaleRU: {title: "Новый запрос места", body: "Пассажир запросил место в вашей поездке."},
		LocaleEN: {title: "New seat request", body: "A passenger requested a seat on your trip."},
	},
	"booking_approved": {
		LocaleTK: {title: "Haýyş tassyklandy", body: "Sürüji ýer haýşyňyzy tassyklady."},
		LocaleRU: {title: "Запрос подтверждён", body: "Водитель подтвердил ваш запрос места."},
		LocaleEN: {title: "Request approved", body: "The driver approved your seat request."},
	},
	"booking_rejected": {
		LocaleTK: {title: "Haýyş ret edildi", body: "Sürüji ýer haýşyňyzy ret etdi."},
		LocaleRU: {title: "Запрос отклонён", body: "Водитель отклонил ваш запрос места."},
		LocaleEN: {title: "Request declined", body: "The driver declined your seat request."},
	},
	"booking_cancelled": {
		LocaleTK: {title: "Bron ýatyryldy", body: "Bu syýahatdaky bron ýatyryldy."},
		LocaleRU: {title: "Бронь отменена", body: "Бронь на эту поездку отменена."},
		LocaleEN: {title: "Booking cancelled", body: "A booking on this trip was cancelled."},
	},
	"trip_cancelled": {
		LocaleTK: {title: "Syýahat ýatyryldy", body: "Sürüji syýahaty ýatyrdy."},
		LocaleRU: {title: "Поездка отменена", body: "Водитель отменил поездку."},
		LocaleEN: {title: "Trip cancelled", body: "The driver cancelled the trip."},
	},
}

// OTPSMS renders the one-time code SMS. The text is ASCII-only so it fits in a
// single GSM-7 segment on every operator.
func OTPSMS(locale, appName, code string) string {
	switch locale {
	case LocaleRU:
		return fmt.Sprintf("%s: kod %s. Nikomu ne soobshchaite ego.", appName, code)
	case LocaleEN:
		return fmt.Sprintf("%s: code %s. Do not share it with anyone.", appName, code)
	default:
		return fmt.Sprintf("%s: kod %s. Kimseyle paylasmanyn.", appName, code)
	}
}

var messages = map[string]map[string]string{
	"BAD_REQUEST": {
		LocaleTK: "Nädogry haýyş.",
		LocaleRU: "Некорректный запрос.",
		LocaleEN: "Malformed request.",
	},
	"VALIDATION_ERROR": {
		LocaleTK: "Maglumatlar nädogry.",
		LocaleRU: "Некорректные данные.",
		LocaleEN: "Invalid request data.",
	},
	"PAYLOAD_TOO_LARGE": {
		LocaleTK: "Haýyş örän uly.",
		LocaleRU: "Слишком большой запрос.",
		LocaleEN: "Request body is too large.",
	},
	"INVALID_PHONE": {
		LocaleTK: "Telefon belgisi nädogry.",
		LocaleRU: "Неверный номер телефона.",
		LocaleEN: "Invalid phone number.",
	},
	"OTP_INVALID": {
		LocaleTK: "Kod nädogry.",
		LocaleRU: "Неверный код.",
		LocaleEN: "Invalid code.",
	},
	"OTP_EXPIRED": {
		LocaleTK: "Koduň möhleti geçdi. Täze kod soraň.",
		LocaleRU: "Срок действия кода истёк. Запросите новый код.",
		LocaleEN: "The code has expired. Request a new one.",
	},
	"OTP_ATTEMPTS_EXCEEDED": {
		LocaleTK: "Synanyşyklaryň sany gutardy. Täze kod soraň.",
		LocaleRU: "Превышено число попыток. Запросите новый код.",
		LocaleEN: "Too many attempts. Request a new code.",
	},
	"RATE_LIMITED": {
		LocaleTK: "Haýyşlar örän köp. Soňrak synanyşyň.",
		LocaleRU: "Слишком много запросов. Попробуйте позже.",
		LocaleEN: "Too many requests. Try again later.",
	},
	"UNAUTHORIZED": {
		LocaleTK: "Ulgama girmek gerek.",
		LocaleRU: "Требуется авторизация.",
		LocaleEN: "Authentication required.",
	},
	"FORBIDDEN": {
		LocaleTK: "Rugsat ýok.",
		LocaleRU: "Доступ запрещён.",
		LocaleEN: "Access denied.",
	},
	"USER_BLOCKED": {
		LocaleTK: "Hasabyňyz petiklenen.",
		LocaleRU: "Ваш аккаунт заблокирован.",
		LocaleEN: "Your account is blocked.",
	},
	"REFRESH_TOKEN_INVALID": {
		LocaleTK: "Sessiýa nädogry. Täzeden giriň.",
		LocaleRU: "Сессия недействительна. Войдите заново.",
		LocaleEN: "Session is invalid. Sign in again.",
	},
	"REFRESH_TOKEN_EXPIRED": {
		LocaleTK: "Sessiýanyň möhleti geçdi. Täzeden giriň.",
		LocaleRU: "Срок сессии истёк. Войдите заново.",
		LocaleEN: "Session has expired. Sign in again.",
	},
	"NOT_FOUND": {
		LocaleTK: "Tapylmady.",
		LocaleRU: "Не найдено.",
		LocaleEN: "Resource not found.",
	},
	"METHOD_NOT_ALLOWED": {
		LocaleTK: "Bu usul rugsat berilmedik.",
		LocaleRU: "Метод не поддерживается.",
		LocaleEN: "Method not allowed.",
	},
	"INTERNAL": {
		LocaleTK: "Serwerde ýalňyşlyk ýüze çykdy.",
		LocaleRU: "Внутренняя ошибка сервера.",
		LocaleEN: "Internal server error.",
	},
	"SMS_NOT_FOUND": {
		LocaleTK: "SMS habary tapylmady.",
		LocaleRU: "SMS-сообщение не найдено.",
		LocaleEN: "SMS message not found.",
	},
	"GATEWAY_EXISTS": {
		LocaleTK: "Bu atly şlýuz eýýäm bar.",
		LocaleRU: "Шлюз с таким именем уже существует.",
		LocaleEN: "A gateway with this name already exists.",
	},
	"PHOTO_TOO_LARGE": {
		LocaleTK: "Surat faýly örän uly.",
		LocaleRU: "Файл изображения слишком большой.",
		LocaleEN: "The image file is too large.",
	},
	"PHOTO_TYPE_UNSUPPORTED": {
		LocaleTK: "Surat JPEG, PNG ýa-da WebP bolmaly.",
		LocaleRU: "Допустимы только изображения JPEG, PNG или WebP.",
		LocaleEN: "Only JPEG, PNG or WebP images are allowed.",
	},
	"VEHICLE_LIMIT": {
		LocaleTK: "Ulag sany çäkden geçdi.",
		LocaleRU: "Достигнут предел числа автомобилей.",
		LocaleEN: "You have reached the vehicle limit.",
	},
	"SEAT_UNAVAILABLE": {
		LocaleTK: "Boş ýer ýok.",
		LocaleRU: "Нет свободных мест.",
		LocaleEN: "No seats left.",
	},
	"TRIP_NOT_FOUND": {
		LocaleTK: "Syýahat tapylmady.",
		LocaleRU: "Поездка не найдена.",
		LocaleEN: "Trip not found.",
	},
	"BOOKING_NOT_FOUND": {
		LocaleTK: "Bron tapylmady.",
		LocaleRU: "Бронирование не найдено.",
		LocaleEN: "Booking not found.",
	},
	"INVALID_STATE": {
		LocaleTK: "Bu hereket häzirki ýagdaýda mümkin däl.",
		LocaleRU: "Это действие сейчас недоступно.",
		LocaleEN: "This action is not allowed in the current state.",
	},
	"BOOKING_EXISTS": {
		LocaleTK: "Bu syýahata eýýäm işjeň bron bar.",
		LocaleRU: "У вас уже есть активная бронь на эту поездку.",
		LocaleEN: "You already have an active booking on this trip.",
	},
	"REVIEW_EXISTS": {
		LocaleTK: "Bu syýahat üçin syn eýýäm berildi.",
		LocaleRU: "Вы уже оставили отзыв по этой поездке.",
		LocaleEN: "You already reviewed this trip.",
	},
	"REVIEW_WINDOW_CLOSED": {
		LocaleTK: "Syn bermegiň möhleti geçdi.",
		LocaleRU: "Срок для отзыва истёк.",
		LocaleEN: "The review window has closed.",
	},
}
