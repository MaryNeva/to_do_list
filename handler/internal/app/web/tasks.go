package web

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	tasks "to-do-list.com/tasks/pkg/domain"
	"to-do-list.com/tasks/pkg/interfaces/payload"
)

type taskController struct {
	taskUC tasks.TaskUC
}

func (t *taskController) create(ctx *fiber.Ctx) error {
	var req payload.Task
	if err := ctx.BodyParser(&req); err != nil {
		return err
	}

	request := payload.ToTaskDomain(req)

	task, err := t.taskUC.CreateTask(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(task)
}

func (t *taskController) get(ctx *fiber.Ctx) error {
	id, err := strconv.Atoi(ctx.Params("id"))

	task, err := t.taskUC.GetTask(ctx.Context(), id)
	if err != nil {
		return err
	}

	return ctx.JSON(task)
}

func (t *taskController) list(ctx *fiber.Ctx) error {
	tasksList, err := t.taskUC.GetListTasks(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(payload.ToListPayload(tasksList))
}

func (t *taskController) update(ctx *fiber.Ctx) error {
	id, err := strconv.Atoi(ctx.Params("id"))

	var req payload.Task
	if err := ctx.BodyParser(&req); err != nil {
		return err
	}

	request := payload.ToTaskDomain(req)

	task, err := t.taskUC.UpdateTask(ctx.Context(), id, request)
	if err != nil {
		return err
	}

	return ctx.JSON(payload.ToTaskPayload(task))
}

func (t *taskController) delete(ctx *fiber.Ctx) error {
	id, err := strconv.Atoi(ctx.Params("id"))

	err = t.taskUC.DeleteTask(ctx.Context(), id)
	if err != nil {
		return err
	}

	return ctx.JSON("deleted")
}

func (t *taskController) toggle(ctx *fiber.Ctx) error {
	id, err := strconv.Atoi(ctx.Params("id"))

	task, err := t.taskUC.ToggleStatus(ctx.Context(), id)
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
