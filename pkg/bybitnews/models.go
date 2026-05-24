package bybitnews

import "time"

// Announcement mirrors Bybit's announcement response item.
type Announcement struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Type        TypeInfo `json:"type"`
	Tags        []string `json:"tags"`
	URL         string   `json:"url"`
	DateTS      int64    `json:"dateTimestamp"`
	StartDateTS int64    `json:"startDateTimestamp"`
	EndDateTS   int64    `json:"endDateTimestamp"`
}

// TypeInfo is the nested type object.
type TypeInfo struct {
	Title string `json:"title"`
	Key   string `json:"key"`
}

// DBAnnouncement is the database row.
type DBAnnouncement struct {
	ID              int       `db:"id"`
	AnnouncementID  string    `db:"announcement_id"`
	Title           string    `db:"title"`
	Description     *string   `db:"description"`
	TypeKey         *string   `db:"type_key"`
	TypeTitle       *string   `db:"type_title"`
	Tags            []string  `db:"tags"`
	URL             *string   `db:"url"`
	DateTS          *int64    `db:"date_ts"`
	StartDateTS     *int64    `db:"start_date_ts"`
	EndDateTS       *int64    `db:"end_date_ts"`
	IsNewListing    bool       `db:"is_new_listing"`
	IsDelisting     bool       `db:"is_delisting"`
	Symbols         []string   `db:"symbols"`
	Markets         []string   `db:"markets"`
	MaxLeverage     *string    `db:"max_leverage"`
	LaunchAt        *time.Time `db:"launch_at"`
	IsPreMarket     bool       `db:"is_pre_market"`
	ParsedAt        *time.Time `db:"parsed_at"`
	CreatedAt       time.Time  `db:"created_at"`
}

// Snapshot is used for API responses.
type Snapshot struct {
	ID             int      `json:"id"`
	AnnouncementID string   `json:"announcement_id"`
	Title          string   `json:"title"`
	Description    string   `json:"description,omitempty"`
	TypeKey        string   `json:"type_key,omitempty"`
	TypeTitle      string   `json:"type_title,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	URL            string   `json:"url,omitempty"`
	DateTS         int64    `json:"date_ts,omitempty"`
	IsNewListing   bool     `json:"is_new_listing"`
	IsDelisting    bool     `json:"is_delisting"`
	Symbols        []string `json:"symbols,omitempty"`
	Markets        []string `json:"markets,omitempty"`
	MaxLeverage    string   `json:"max_leverage,omitempty"`
	LaunchAt       int64    `json:"launch_at,omitempty"`
	IsPreMarket    bool     `json:"is_pre_market"`
	CreatedAt      string   `json:"created_at"`
}
