package push

import (
	"strings"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// texts mirror the app's inbox sentences (yolugra lib/app/localization/arb, keys note*),
// so a push reads the same as its row in the inbox. Order: tk, ru, en.
var texts = map[ride.NotificationType][3]string{
	ride.NoteBookingRequested: {
		"{name} {route} syýahatynda orun soraýar",
		"{name} запрашивает места: {route}",
		"{name} asks for seats on {route}",
	},
	ride.NoteBookingApproved: {
		"{route} syýahaty üçin haýyşyňyz tassyklandy",
		"Ваша заявка на поездку {route} подтверждена",
		"Your request for {route} was accepted",
	},
	ride.NoteBookingRejected: {
		"{route} syýahaty üçin haýyşyňyz ret edildi",
		"Ваша заявка на поездку {route} отклонена",
		"Your request for {route} was declined",
	},
	ride.NoteBookingCancelledP: {
		"{name} {route} syýahatyndaky bronuny ýatyrdy",
		"{name} отменил(а) бронирование: {route}",
		"{name} cancelled the booking on {route}",
	},
	ride.NoteBookingCancelledD: {
		"Sürüji {route} syýahatyndaky bronuňyzy ýatyrdy",
		"Водитель отменил ваше бронирование: {route}",
		"The driver cancelled your booking on {route}",
	},
	ride.NoteBookingExpired: {
		"{route} syýahaty üçin haýyşyňyzyň jogapsyz möhleti geçdi",
		"Заявка на поездку {route} истекла без ответа",
		"Your request for {route} expired without an answer",
	},
	ride.NoteTripCancelled: {
		"{route} syýahaty ýatyryldy",
		"Поездка {route} отменена",
		"The trip {route} was cancelled",
	},
	ride.NoteTripUpdated: {
		"Sürüji {route} syýahatyny üýtgetdi",
		"Водитель изменил поездку {route}",
		"The driver updated the trip {route}",
	},
	ride.NoteTripReminder: {
		"{route} syýahaty ýakynda başlaýar",
		"Поездка {route} скоро начнётся",
		"The trip {route} starts soon",
	},
	ride.NoteTripCompleted: {
		"{route} syýahaty tamamlandy. Ýol ýoldaşlaryňyza baha beriň",
		"Поездка {route} завершена. Оцените попутчиков",
		"The trip {route} is completed. Rate your travel companions",
	},
	ride.NoteReviewReceived: {
		"{name} size syn galdyrdy",
		"{name} оставил(а) вам отзыв",
		"{name} left you a review",
	},
}

var someone = [3]string{"Ulanyjy", "Пользователь", "A user"}

func langIndex(locale string) int {
	switch locale {
	case "ru":
		return 1
	case "en":
		return 2
	default:
		return 0
	}
}

func placeName(l *ride.Location, i int) string {
	if l == nil {
		return ""
	}
	names := [3]string{l.NameTK, l.NameRU, l.NameEN}
	if names[i] != "" {
		return names[i]
	}
	return l.NameTK
}

// Build returns the push for j, or false for a type that has no text.
func Build(title string, j ride.PushJob) (Message, bool) {
	tmpl, ok := texts[j.Type]
	if !ok {
		return Message{}, false
	}
	i := langIndex(j.Locale)
	route := ""
	if from, to := placeName(j.From, i), placeName(j.To, i); from != "" && to != "" {
		route = from + " → " + to
	}
	name := someone[i]
	if j.Actor != nil {
		if full := strings.TrimSpace(j.Actor.FirstName + " " + j.Actor.LastName); full != "" {
			name = full
		}
	}
	body := strings.NewReplacer("{name}", name, "{route}", route).Replace(tmpl[i])
	data := map[string]string{"notification_id": j.NotificationID, "type": string(j.Type)}
	if j.TripID != "" {
		data["trip_id"] = j.TripID
	}
	if j.BookingID != "" {
		data["booking_id"] = j.BookingID
	}
	return Message{Title: title, Body: strings.Join(strings.Fields(body), " "), Data: data}, true
}
