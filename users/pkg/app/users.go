package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	"to-do-list.com/users/password_service"
	users "to-do-list.com/users/pkg/domain"
	"to-do-list.com/users/token"
)

type authorization struct {
	service   password_service.PasswordService
	timeout   time.Duration
	userStore users.UserStore
	ts        token.Service
}

func (a *authorization) Login(ctx context.Context, auth users.AuthRequest) (user users.User, err error) {
	if a.service.LoginByConfig(ctx, auth) {
		id, err := strconv.Atoi("0")
		if err != nil {
			return user, err
		}
		return users.User{
			Id:       id,
			Username: "admin",
		}, nil
	}

	user, err = a.userStore.LoginUser(ctx, auth.Username)
	if err != nil {

		return user, err
	}

	if !a.service.LoginByDB(ctx, auth, user) {
		err = errors.New("authentication failed: wrong password or user mismatch")
		return users.User{}, err
	}

	return user, err
}

func (a *authorization) Authenticate(ctx context.Context, auth users.AuthRequest) (users.Token, error) {

	user, err := a.Login(ctx, auth)
	if err != nil {
		return "", err
	}

	token, err := a.ts.GenerateToken(user)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (a *authorization) ValidateToken(ctx context.Context, token users.Token) (users.User, error) {
	user, err := a.ts.ValidateToken(string(token))
	if err != nil {
		return users.User{}, err
	}

	return user, nil
}

func NewAuthControl(service password_service.PasswordService, userStore users.UserStore, ts token.Service, timeout time.Duration) users.AuthService {
	return &authorization{service: service, userStore: userStore, ts: ts, timeout: timeout}
}

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

func NewUserControl(store users.UserStore, duration time.Duration) users.UserUC {
	return &userStore{
		db:       store,
		duration: duration,
	}
}
