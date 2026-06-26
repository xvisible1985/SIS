// services/parser/bybit_news.go
package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/bybitnews"
)

func runBybitNews(ctx context.Context, pool *pgxpool.Pool) {
	scraper := bybitnews.NewScraper(pool)
	scraper.Start(ctx)
}
