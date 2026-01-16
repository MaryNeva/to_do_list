package password_service

import (
	"context"

	"golang.org/x/crypto/bcrypt"
	"to-do-list.com/users/pkg/domain"
)

type PasswordService interface {
	HashPassword(password string) (string, error)
	CheckPassword(inputPassword, storedHash string) error
	LoginByConfig(ctx context.Context, auth domain.AuthRequest) bool
	LoginByDB(ctx context.Context, request domain.AuthRequest, user domain.User) bool
}

type passwordService struct {
	username     string
	userPassword string
}

func (p *passwordService) HashPassword(password string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	return string(hashedPassword), nil
}

func (p *passwordService) CheckPassword(inputPassword, storedHash string) error {
	err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(inputPassword))
	if err != nil {
		return err
	}

	return nil
}

func (p *passwordService) LoginByConfig(ctx context.Context, auth domain.AuthRequest) bool {
	if auth.Username == p.username && p.CheckPassword(auth.Password, p.userPassword) == nil {
		return true
	}

	return false
}

func (p *passwordService) LoginByDB(ctx context.Context, request domain.AuthRequest, user domain.User) bool {
	if request.Username == user.Username && p.CheckPassword(request.Password, user.Password) == nil {

		return true
	}

	return false
}

func NewPasswordService(name string, password string) PasswordService {
	return &passwordService{username: name, userPassword: password}
}
