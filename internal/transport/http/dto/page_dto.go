package dto

import "to-do-list/internal/domain"

type PageResponse[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func ToTaskPageResponse(page domain.Page[domain.Task]) PageResponse[TaskResponse] {
	return PageResponse[TaskResponse]{
		Items:  ToTaskListResponse(page.Items),
		Total:  page.Total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}
}

func ToUserPageResponse(page domain.Page[domain.User]) PageResponse[UserResponse] {
	return PageResponse[UserResponse]{
		Items:  ToUserListResponse(page.Items),
		Total:  page.Total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}
}
