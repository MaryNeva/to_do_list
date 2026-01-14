module to-do-list.com/handler

go 1.25.0

replace (
	to-do-list.com/tasks => ../tasks
	to-do-list.com/users => ../users
	to-do-list.com/utils => ../utils
)

require (
	github.com/gofiber/fiber/v2 v2.52.10
	github.com/golang-migrate/migrate/v4 v4.19.1
	github.com/jackc/pgx/v5 v5.8.0
	gopkg.in/yaml.v3 v3.0.1
	to-do-list.com/tasks v0.0.0-00010101000000-000000000000
	to-do-list.com/users v0.0.0-00010101000000-000000000000
	to-do-list.com/utils v0.0.0-00010101000000-000000000000
)

require (
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.51.0 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
)
