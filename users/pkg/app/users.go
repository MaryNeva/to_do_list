package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"
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

func (a *authorization) CreateUser(ctx context.Context, req users.User) (users.User, error) {
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	req.Password = string(hashedPassword)

	user, err := a.userStore.CreateUser(ctx, req)
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

func (u *userStore) GetList(ctx context.Context) ([]users.User, error) {
	users, err := u.db.GetList(ctx)
	if err != nil {
		return nil, err
	}

	return users, nil
}

func (u *userStore) UpdateUser(ctx context.Context, id int, req users.User) (users.User, error) {
	user, err := u.db.GetUser(ctx, id)
	if err != nil {
		return users.User{}, err
	}

	if req.Username == "" {
		req.Username = user.Username
	}

	if req.Email == "" {
		req.Email = user.Email
	}

	if req.Password == "" {
		req.Password = user.Password
	}

	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	req.Password = string(hashedPassword)

	fmt.Println(req.Password)

	err = u.db.UpdateUser(ctx, id, req)
	if err != nil {
		return users.User{}, err
	}

	user, err = u.db.GetUser(ctx, id)
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
