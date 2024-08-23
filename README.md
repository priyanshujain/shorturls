# ShortURL

Creates a short URL from a long URL.

## Installation

1. Clone the repository
2. Run `go mod download`
3. Create .set DATABASE_URL environment variable
```bash
export DATABASE_URL="postgres://app:app@localhost:5432/app"

```
4. Create urls table in the database
```sql
CREATE TABLE urls (
    short_link TEXT PRIMARY KEY,
    long_url TEXT NOT NULL,
    create_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    ip TEXT NOT NULL
);
```
5. Run `go run main.go`

