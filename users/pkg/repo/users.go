package repo

import "to-do-list.com/users/pkg/domain"

type UserModel struct {
	Id        int
	Username  string
	Email     string
	Password  string
	ListModel []int
}

func toUserModel(user domain.User) UserModel {
	return UserModel{
		Id:        user.Id,
		Username:  user.Username,
		Email:     user.Email,
		Password:  user.Password,
		ListModel: user.ListTasks,
	}
}

func toUserDomain(user UserModel) domain.User {
	return domain.User{
		Id:        user.Id,
		Username:  user.Username,
		Email:     user.Email,
		Password:  user.Password,
		ListTasks: user.ListModel,
	}
}
