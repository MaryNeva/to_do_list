package web

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	users "to-do-list.com/users/pkg/domain"
	"to-do-list.com/users/pkg/interfaces/payload"
)

type authController struct {
	authUC users.AuthService
}

func (a *authController) login(ctx *fiber.Ctx) error {
	var req payload.AuthRequest
	if err := ctx.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	err := payload.Validate(req)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	request := payload.RequestToAuth(req)

	token, err := a.authUC.Authenticate(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(payload.AuthToken{Token: token})
}

func (a *authController) validateToken(ctx *fiber.Ctx) error {
	var token payload.AuthToken

	err := json.Unmarshal(ctx.Body(), &token)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	user, err := a.authUC.ValidateToken(ctx.Context(), payload.FromDTOToken(token))
	if err != nil {
		return err
	}

	return ctx.JSON(payload.AuthValidate{
		AuthToken: payload.AuthToken{
			Token: token.Token,
		},
		User: payload.ToUserPayload(user),
	})
}

func AuthEndpoint(router fiber.Router, authUC users.AuthService) {
	ctrl := authController{
		authUC: authUC,
	}

	auth := router.Group("/")
	auth.Post("/login", ctrl.login)
	auth.Get("/validate", ctrl.validateToken)
}

type userController struct {
	userUC users.UserUC
}

func (u *userController) create(ctx *fiber.Ctx) error {
	var req payload.User
	if err := ctx.JSON(&req); err != nil {
		return err
	}

	request := payload.ToUserDomain(req)

	task, err := u.userUC.CreateUser(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(task)
}

func (u *userController) get(ctx *fiber.Ctx) error {
	var req payload.User
	if err := ctx.JSON(&req); err != nil {
		return err
	}

	task, err := u.userUC.GetUser(ctx.Context(), req.Id)
	if err != nil {
		return err
	}

	return ctx.JSON(task)
}

func (u *userController) update(ctx *fiber.Ctx) error {
	var req payload.User
	if err := ctx.JSON(&req); err != nil {
		return err
	}

	request := payload.ToUserDomain(req)

	task, err := u.userUC.UpdateUser(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(task)
}

func (u *userController) delete(ctx *fiber.Ctx) error {
	var req payload.User
	if err := ctx.JSON(&req); err != nil {
		return err
	}

	err := u.userUC.DeleteUser(ctx.Context(), req.Id)
	if err != nil {
		return err
	}

	return ctx.JSON("deleted")
}

func UserEndpoint(router fiber.Router, usersUC users.UserUC) {
	ctrl := userController{
		userUC: usersUC,
	}

	user := router.Group("/")
	user.Post("/new", ctrl.create)
	user.Get("/:id", ctrl.get)
	user.Delete("/:id", ctrl.delete)
	user.Patch("/:id", ctrl.update)
}
