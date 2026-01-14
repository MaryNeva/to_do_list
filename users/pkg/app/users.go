package app

import (
	"context"
	"time"

	users "to-do-list.com/users/pkg/domain"
)

type userStore struct {
	db       users.UserStore
	duration time.Duration
}

func (u *userStore) GetUser(ctx context.Context, id int) (users.User, error) {
	user, err := u.db.GetUser(ctx, id)
	if err != nil {
		return users.User{}, err
	}

	return user, nil
}

func (u *userStore) CreateUser(ctx context.Context, user users.User) (users.User, error) {
	user, err := u.db.CreateUser(ctx, user)
	if err != nil {
		return users.User{}, err
	}

	return user, nil
}

func (u *userStore) UpdateUser(ctx context.Context, user users.User) (users.User, error) {
	err := u.db.UpdateUser(ctx, user)
	if err != nil {
		return users.User{}, err
	}

	user, err = u.db.GetUser(ctx, user.Id)
	if err != nil {
		return users.User{}, err
	}

	return user, nil
}

func (u *userStore) DeleteUser(ctx context.Context, id int) error {
	return u.db.DeleteUser(ctx, id)
}

func NewUserControl(store users.UserStore, duration time.Duration) userStore {
	return userStore{
		db:       store,
		duration: duration,
	}
}
