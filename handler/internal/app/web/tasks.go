package web

import (
	"github.com/gofiber/fiber/v2"
	tasks "to-do-list.com/tasks/pkg/domain"
	"to-do-list.com/tasks/pkg/interfaces/payload"
)

type taskController struct {
	taskUC tasks.TaskUC
}

func (t *taskController) create(ctx *fiber.Ctx) error {
	var req payload.Task

	if err := ctx.JSON(&req); err != nil {
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
	var req payload.Task

	if err := ctx.JSON(&req); err != nil {
		return err
	}

	task, err := t.taskUC.GetTask(ctx.Context(), req.Id)
	if err != nil {
		return err
	}

	return ctx.JSON(task)
}

func (t *taskController) list(ctx *fiber.Ctx) error {
	users, err := t.taskUC.GetListTasks(ctx.Context())
	if err != nil {
		return err
	}
	return ctx.JSON(users)
}

func (t *taskController) update(ctx *fiber.Ctx) error {
	var req payload.Task
	if err := ctx.JSON(&req); err != nil {
		return err
	}
	request := payload.ToTaskDomain(req)

	task, err := t.taskUC.UpdateTask(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(task)
}

func (t *taskController) delete(ctx *fiber.Ctx) error {
	var req payload.Task
	if err := ctx.JSON(&req); err != nil {
		return err
	}
	err := t.taskUC.DeleteTask(ctx.Context(), req.Id)
	if err != nil {
		return err
	}

	return ctx.JSON("deleted")
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
}
