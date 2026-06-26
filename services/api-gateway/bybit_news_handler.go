package main

import (
	"net/http"
	"strconv"

	"sis/pkg/bybitnews"
)

func dbToSnapshot(a bybitnews.DBAnnouncement) bybitnews.Snapshot {
	s := bybitnews.Snapshot{
		ID:             a.ID,
		AnnouncementID: a.AnnouncementID,
		Title:          a.Title,
		Tags:           a.Tags,
		IsNewListing:   a.IsNewListing,
		IsDelisting:    a.IsDelisting,
		Symbols:        a.Symbols,
		Markets:        a.Markets,
		IsPreMarket:    a.IsPreMarket,
		CreatedAt:      a.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if a.Description != nil {
		s.Description = *a.Description
	}
	if a.TypeKey != nil {
		s.TypeKey = *a.TypeKey
	}
	if a.TypeTitle != nil {
		s.TypeTitle = *a.TypeTitle
	}
	if a.URL != nil {
		s.URL = *a.URL
	}
	if a.DateTS != nil {
		s.DateTS = *a.DateTS
	}
	if a.MaxLeverage != nil {
		s.MaxLeverage = *a.MaxLeverage
	}
	if a.LaunchAt != nil {
		s.LaunchAt = a.LaunchAt.UnixMilli()
	}
	return s
}

// ListBybitAnnouncements returns announcements from the DB.
func (s *Server) ListBybitAnnouncements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	typ := q.Get("type")
	onlyListings := q.Get("listings") == "1"
	onlyDelistings := q.Get("delistings") == "1"

	rows, err := bybitnews.ListFromDB(r.Context(), s.pool, limit, typ, onlyListings, onlyDelistings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]bybitnews.Snapshot, 0, len(rows))
	for _, a := range rows {
		out = append(out, dbToSnapshot(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetLatestBybitNews returns the latest listing/delisting announcements.
func (s *Server) GetLatestBybitNews(w http.ResponseWriter, r *http.Request) {
	rows, err := bybitnews.LatestFromDB(r.Context(), s.pool, 5)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]bybitnews.Snapshot, 0, len(rows))
	for _, a := range rows {
		out = append(out, dbToSnapshot(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// RefreshBybitNews is now handled by the parser service.
// This endpoint is kept for backward compatibility but returns 501.
func (s *Server) RefreshBybitNews(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "refresh is handled by the parser service")
}

// GetDelistingSymbols returns all symbols currently scheduled for delisting.
func (s *Server) GetDelistingSymbolsHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"symbols": s.GetDelistingSymbols()})
}
