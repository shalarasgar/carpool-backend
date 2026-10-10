package dto

// Location is one directory entry. Name is localized from Accept-Language.
type Location struct {
	ID        string  `json:"id"`
	ParentID  *string `json:"parent_id"`
	Type      string  `json:"type"`
	Name      string  `json:"name"`
	IsActive  bool    `json:"is_active"`
	SortOrder int     `json:"sort_order"`
}

// LocationList is GET /v1/locations.
type LocationList struct {
	Locations []Location `json:"locations"`
}
