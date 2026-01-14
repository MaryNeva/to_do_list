package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/log"
	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5/pgxpool"
	"to-do-list.com/handler/internal/app/middleware"
	"to-do-list.com/handler/internal/app/web"
	"to-do-list.com/handler/internal/config"
	"to-do-list.com/users/token"
	"to-do-list.com/utils/dep"

	tasks "to-do-list.com/tasks/pkg/app"
	tasksdb "to-do-list.com/tasks/pkg/repo"
	users "to-do-list.com/users/pkg/app"
	usersdb "to-do-list.com/users/pkg/repo"
)

var (
	Version   string
	BuildTime string
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Printf("Version: %s\nBuild Time: %s\n", Version, BuildTime)
			return
		}
	}

	var webApp *fiber.App

	webApp.Use(func(c *fiber.Ctx) error {
		c.Set("X-Version", Version)
		return c.Next()
	})

	ctx := context.Background()

	var (
		configsDir   string
		migrationDir string
	)

	flag.StringVar(&configsDir, "configs", "../configs", "path to the admin-config directory")
	flag.StringVar(&migrationDir, "migrate", "./migrations", "path to the migration directory")
	flag.Parse()

	var cfg config.Config

	cfg, err := config.LoadConfig[config.Config](configsDir)
	if err != nil {
		panic(err)
	}

	dsn := cfg.Handler.Repository.GetPath()

	Migrate(migrationDir, dsn)

	postgresDB, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("Error connecting to DB: %v", err)
	}

	err = postgresDB.Ping(ctx)
	if err != nil {
		panic(err)
	}

	log.Info("Connected to the database successfully.")

	taskStore, err := tasksdb.NewTaskStore(ctx, postgresDB)
	if err != nil {
		panic(err)
	}

	userStore, err := usersdb.NewUserStore(ctx, postgresDB)
	if err != nil {
		panic(err)
	}

	//healthStore, err := postgresdb.NewHealthStore(ctx, postgresDB)
	//if err != nil {
	//	panic(err)
	//}

	tokenService := token.NewTokenService(cfg.Handler.Security.JWT.ExpiresIn, cfg.Handler.Security.JWT.Secret)
	middlewareJWT := middleware.NewTokenMiddleware(tokenService, cfg.Handler.Security.JWT.Secret)
	//
	//passwordService := password_service.NewPasswordService(cfg.Admin.Authorization.Name, cfg.Admin.Authorization.Password)

	//loginFn := authorization.NewLogin(authStore, passwordService, time.Second)

	//app.NewInstance(
	//	authStore,
	//	userStore,
	//	taskStore,
	//	healthStore)

	//http.HealthcheckEndpoint(webApp.Group("/healthcheck/admin", func(ctx *fiber.Ctx) error {
	//	token := ctx.Get("Sanitation")
	//	if token != cfg.Handler.HealthSecret {
	//		return fiber.NewError(fiber.StatusNotFound)
	//	}
	//
	//	return ctx.Next()
	//}), dep.ProvideMany(
	//	app.NewHealthcheck(),
	//))

	api := JsonRestApi(webApp.Group("/api"))

	//web.TaskEndpoint(api.Group("/auth"), dep.ProvideMany(
	//	authorization.NewAuthenticate(loginFn, tokenService, time.Second),
	//	authorization.NewValidateToken(authStore, tokenService, time.Second),
	//))

	protected := api.Group("", middlewareJWT.JWTMiddleware())

	web.UserEndpoint(protected.Group("/users"), dep.ProvideMany(
		users.NewUserControl(userStore, time.Second),
	))

	web.TaskEndpoint(protected.Group("/tasks"), dep.ProvideMany(
		tasks.NewTaskControl(taskStore, time.Second),
	))

	if err := ListenAndServe(webApp, cfg.Handler.App.Address); err != nil {
		panic(err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	<-stop

}

func ListenAndServe(app *fiber.App, addr string) error {
	if err := app.Listen(addr); err != nil {
		return err
	}

	return nil
}

func Migrate(pathToFile, connectionStr string) {
	m, err := migrate.New(fmt.Sprintf("file://%s", pathToFile), connectionStr)
	if err != nil {
		panic(err)
	}

	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		panic(err)
	}
}

func JsonRestApi(router fiber.Router) fiber.Router {
	router.Use(func(ctx *fiber.Ctx) error {
		ctx.Set("Content-Type", "application/json")
		ctx.Accepts("application/json")

		return ctx.Next()
	})

	return router
}
