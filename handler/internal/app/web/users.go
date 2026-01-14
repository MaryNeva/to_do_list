package web

import (
	"github.com/gofiber/fiber/v2"
	users "to-do-list.com/users/pkg/domain"
	"to-do-list.com/users/pkg/interfaces/payload"
	"to-do-list.com/utils/dep"
)

type userController struct {
	createUser users.CreateUser
	getUser    users.GetUser
	updateUser users.UpdateUser
	deleteUser users.DeleteUser
}

func (u *userController) create(ctx *fiber.Ctx) error {
	var req payload.User

	if err := ctx.JSON(&req); err != nil {
		return err
	}

	request := payload.ToUserDomain(req)

	task, err := u.createUser(ctx.Context(), request)
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

	task, err := u.getUser(ctx.Context(), req.Id)
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

	task, err := u.updateUser(ctx.Context(), request)
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
	err := u.deleteUser(ctx.Context(), req.Id)
	if err != nil {
		return err
	}

	return ctx.JSON("deleted")
}

func UserEndpoint(router fiber.Router, deps []any) {
	ctrl := userController{
		createUser: dep.MustInject[users.CreateUser](deps),
		getUser:    dep.MustInject[users.GetUser](deps),
		updateUser: dep.MustInject[users.UpdateUser](deps),
		deleteUser: dep.MustInject[users.DeleteUser](deps),
	}

	user := router.Group("/")
	user.Post("/new", ctrl.create)
	user.Get("/:id", ctrl.get)
	user.Delete("/:id", ctrl.delete)
	user.Patch("/:id", ctrl.update)
}
