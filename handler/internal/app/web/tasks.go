package web

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	tasks "to-do-list.com/tasks/pkg/domain"
	"to-do-list.com/tasks/pkg/interfaces/payload"
	users "to-do-list.com/users/pkg/domain"
)

type taskController struct {
	taskUC tasks.TaskUC
}

func (t *taskController) create(ctx *fiber.Ctx) error {
	var req payload.Task
	if err := ctx.BodyParser(&req); err != nil {
		return err
	}

	user, err := getUser(ctx)
	if err != nil {
		return err
	}

	req.Creator = user.Id
	request := payload.ToTaskDomain(req)

	task, err := t.taskUC.CreateTask(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(payload.ToTaskPayload(task))
}

func (t *taskController) get(ctx *fiber.Ctx) error {
	id, err := strconv.Atoi(ctx.Params("id"))
	if err != nil {
		return ctx.SendStatus(fiber.StatusBadRequest)
	}

	user, err := getUser(ctx)
	if err != nil {
		return err
	}

	task, err := t.taskUC.GetTask(ctx.Context(), id, user.Id)
	if err != nil {
		return err
	}

	return ctx.JSON(payload.ToTaskPayload(task))
}

func (t *taskController) list(ctx *fiber.Ctx) error {
	user, err := getUser(ctx)
	if err != nil {
		return err
	}

	tasksList, err := t.taskUC.GetListTasks(ctx.Context(), user.Id)
	if err != nil {
		return err
	}

	return ctx.JSON(payload.ToListPayload(tasksList))
}

func (t *taskController) update(ctx *fiber.Ctx) error {
	user, err := getUser(ctx)
	if err != nil {
		return err
	}

	id, err := strconv.Atoi(ctx.Params("id"))
	if err != nil {
		return ctx.SendStatus(fiber.StatusBadRequest)
	}

	var req payload.Task
	if err := ctx.BodyParser(&req); err != nil {
		return err
	}

	request := payload.ToTaskDomain(req)

	task, err := t.taskUC.UpdateTask(ctx.Context(), id, user.Id, request)
	if err != nil {
		return err
	}

	return ctx.JSON(payload.ToTaskPayload(task))
}

func (t *taskController) delete(ctx *fiber.Ctx) error {
	id, err := strconv.Atoi(ctx.Params("id"))
	if err != nil {
		return ctx.SendStatus(fiber.StatusBadRequest)
	}

	user, err := getUser(ctx)
	if err != nil {
		return err
	}

	err = t.taskUC.DeleteTask(ctx.Context(), id, user.Id)
	if err != nil {
		return err
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

func (t *taskController) toggle(ctx *fiber.Ctx) error {
	id, err := strconv.Atoi(ctx.Params("id"))
	if err != nil {
		return ctx.SendStatus(fiber.StatusBadRequest)
	}

	user, err := getUser(ctx)
	if err != nil {
		return err
	}

	task, err := t.taskUC.ToggleStatus(ctx.Context(), id, user.Id)
	if err != nil {
		return err
	}

	return ctx.JSON(payload.ToTaskPayload(task))
}

func TaskEndpoint(router fiber.Router, tasksUC tasks.TaskUC) {
	ctrl := taskController{
		taskUC: tasksUC,
	}

	task := router.Group("/")
	task.Post("/new", ctrl.create)
	task.Get("/:id", ctrl.get)
	task.Delete("/:id", ctrl.delete)
	task.Patch("/:id", ctrl.update)
	task.Get("/", ctrl.list)
	task.Post("/:id/toggle-status", ctrl.toggle)
}

func getUser(ctx *fiber.Ctx) (users.User, error) {
	user, ok := ctx.Locals("user").(users.User)
	if !ok {
		return users.User{}, fiber.ErrUnauthorized
	}
	return user, nil
}
