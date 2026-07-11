// Command migrate applies database migrations. Run as a one-off before the
// server starts (see docker-compose.yml's migrate service) rather than at
// server startup, so schema changes are explicit and decoupled from app
// container restarts/crash loops.
package main

import (
	"database/sql"
	"embed"
	"log/slog"
	"os"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL environment variable is required")
		os.Exit(1)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		slog.Error("opening database connection", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		slog.Error("setting goose dialect", "error", err)
		os.Exit(1)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		slog.Error("running migrations", "error", err)
		os.Exit(1)
	}

	slog.Info("migrations applied successfully")
}
