// Package i18n holds the localized text of API error codes in Turkmen, Russian, and English.
// Clients should switch on the stable code; the message is a readable fallback.
package i18n

var messages = map[string][3]string{ // tk, ru, en
	"VALIDATION_ERROR":     {"Maglumatlar nädogry", "Некорректные данные", "Invalid request"},
	"NOT_FOUND":            {"Tapylmady", "Не найдено", "Resource not found"},
	"FORBIDDEN":            {"Bu hereket size rugsat berilmeýär", "Действие запрещено", "You are not allowed to do this"},
	"UNAUTHORIZED":         {"Ulgama giriň", "Требуется вход", "Unauthorized"},
	"USER_BLOCKED":         {"Bu hasap ulanylyp bilinmeýär", "Этот аккаунт заблокирован", "This account cannot sign in"},
	"PROFILE_INCOMPLETE":   {"Adyňyzy we familiýaňyzy giriziň", "Укажите имя и фамилию", "Enter your first and last name first"},
	"TRIP_OVERLAP":         {"Bu wagtda sizde başga syýahat bar", "У вас уже есть поездка в это время", "You already have a trip at this time"},
	"INSUFFICIENT_SEATS":   {"Ýeterlik boş orun ýok", "Недостаточно свободных мест", "Not enough free seats"},
	"TRIP_NOT_ACTIVE":      {"Syýahat elýeterli däl", "Поездка недоступна", "The trip is no longer available"},
	"TRIP_ALREADY_STARTED": {"Syýahat eýýäm başlandy", "Поездка уже началась", "The trip has already started"},
	"TRIP_NOT_STARTED":     {"Syýahat entek başlanmady", "Поездка ещё не началась", "The trip has not started yet"},
	"BOOKING_NOT_PENDING":  {"Haýyş eýýäm seredildi", "Заявка уже рассмотрена", "The request is no longer pending"},
	"INVALID_SEGMENT":      {"Nädogry ugur bölegi", "Неверный участок маршрута", "Invalid route segment"},
	"BOOKING_EXISTS":       {"Bu syýahat üçin haýyşyňyz eýýäm bar", "У вас уже есть заявка на эту поездку", "You already have a request for this trip"},
	"OWN_TRIP":             {"Öz syýahatyňyza ýer sorap bilmeýärsiňiz", "Нельзя бронировать свою поездку", "You cannot book your own trip"},
	"INVALID_TRANSITION":   {"Bu ýagdaýy üýtgedip bolmaýar", "Этот статус нельзя изменить", "This status cannot be changed"},
	"REVIEW_NOT_ALLOWED":   {"Diňe tamamlanan syýahatdan soň baha berip bolýar", "Отзыв можно оставить только после завершённой поездки", "Reviews are only allowed after a completed trip"},
	"REVIEW_EXISTS":        {"Siz eýýäm baha berdiňiz", "Вы уже оставили отзыв", "You have already left a review"},
	"SEATS_BELOW_BOOKED":   {"Orun sany tassyklanan ýerlerden az bolup bilmez", "Мест не может быть меньше уже подтверждённых", "Seats cannot be fewer than already approved"},
	"TRIP_HAS_BOOKINGS":    {"Haýyşlar bar wagty muny üýtgedip bolmaýar", "Нельзя изменить, пока есть заявки", "This cannot change while the trip has requests"},
	"PHONE_TAKEN":          {"Bu belgi başga hasapda ulanylýar", "Этот номер уже используется", "This number is already in use"},
	"LOCATION_INACTIVE":    {"Ýer elýeterli däl", "Населённый пункт недоступен", "The location is not available"},
	"CONTACT_UNAVAILABLE":  {"Telefon belgisi diňe tassyklanandan soň görünýär", "Номер доступен только после подтверждения", "The phone number is shown only after approval"},
	"USER_UNAVAILABLE":     {"Bu ulanyjy bilen syýahat edip bolmaýar", "Поездка с этим пользователем недоступна", "You cannot travel with this user"},
	"FILE_TOO_LARGE":       {"Faýl gaty uly", "Файл слишком большой", "The file is too large"},
	"UNSUPPORTED_MEDIA":    {"Diňe JPEG, PNG ýa-da WebP suratlar", "Только изображения JPEG, PNG или WebP", "Only JPEG, PNG, or WebP images"},
	"RATE_LIMITED":         {"Gaty köp synanyşyk, soňrak gaýtalaň", "Слишком много запросов, попробуйте позже", "Too many requests"},
	"INVALID_PHONE":        {"Türkmenistanyň mobil belgisini giriziň", "Введите мобильный номер Туркменистана", "Phone number is not a supported Turkmenistan mobile number"},
	"INVALID_CODE":         {"Kod nädogry", "Неверный код", "Invalid code"},
	"INTERNAL":             {"Ulgamda ýalňyşlyk", "Внутренняя ошибка сервера", "Internal server error"},
}

// Message returns the localized text of code for locale (tk, ru, or en). Unknown codes
// fall back to the internal error text.
func Message(locale, code string) string {
	m, ok := messages[code]
	if !ok {
		m = messages["INTERNAL"]
	}
	switch locale {
	case "ru":
		return m[1]
	case "en":
		return m[2]
	default:
		return m[0]
	}
}
