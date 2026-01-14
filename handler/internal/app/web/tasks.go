package web

import (
	"github.com/gofiber/fiber/v2"
	tasks "to-do-list.com/tasks/pkg/domain"
	"to-do-list.com/tasks/pkg/interfaces/payload"
	"to-do-list.com/utils/dep"
)

type taskController struct {
	createTask tasks.CreateTask
	getTask    tasks.GetTask
	updateTask tasks.UpdateTask
	deleteTask tasks.DeleteTask
	getList    tasks.GetListTasks
}

func (t *taskController) create(ctx *fiber.Ctx) error {
	var req payload.Task

	if err := ctx.JSON(&req); err != nil {
		return err
	}

	request := payload.ToTaskDomain(req)

	task, err := t.createTask(ctx.Context(), request)
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

	task, err := t.getTask(ctx.Context(), req.Id)
	if err != nil {
		return err
	}

	return ctx.JSON(task)
}

func (t *taskController) update(ctx *fiber.Ctx) error {
	var req payload.Task
	if err := ctx.JSON(&req); err != nil {
		return err
	}
	request := payload.ToTaskDomain(req)

	task, err := t.updateTask(ctx.Context(), request)
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
	err := t.deleteTask(ctx.Context(), req.Id)
	if err != nil {
		return err
	}

	return ctx.JSON("deleted")
}

func TaskEndpoint(router fiber.Router, deps []any) {
	ctrl := taskController{
		createTask: dep.MustInject[tasks.CreateTask](deps),
		getTask:    dep.MustInject[tasks.GetTask](deps),
		updateTask: dep.MustInject[tasks.UpdateTask](deps),
		deleteTask: dep.MustInject[tasks.DeleteTask](deps),
		getList:    dep.MustInject[tasks.GetListTasks](deps),
	}

	task := router.Group("/")
	task.Post("/new", ctrl.create)
	task.Get("/:id", ctrl.get)
	task.Delete("/:id", ctrl.delete)
	task.Patch("/:id", ctrl.update)
}
