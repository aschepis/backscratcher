package migrations

import (
	"database/sql"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/rs/zerolog"
)

// RunMigrations runs all pending migrations from the specified directory.
// It uses golang-migrate to apply migrations to the database.
func RunMigrations(db *sql.DB, migrationsPath string, logger zerolog.Logger) error {
	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
	if err != nil {
		return fmt.Errorf("failed to create sqlite3 driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		fmt.Sprintf("file://%s", migrationsPath),
		"sqlite3",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize migrations: %w", err)
	}

	logger.Info().Str("migrationsPath", migrationsPath).Msg("Running database migrations")
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	if err == migrate.ErrNoChange {
		logger.Info().Msg("Database is already up to date")
	} else {
		logger.Info().Msg("Database migrations applied successfully")
	}

	return nil
}
