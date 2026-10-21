package models

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// OpenPostgres creates a GORM handle for an existing ShopStops database.
// Apply the numbered SQL migrations before using it; this package intentionally
// does not expose an AutoMigrate helper.
func OpenPostgres(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{})
}
