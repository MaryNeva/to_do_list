package token

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"to-do-list.com/users/pkg/domain"
)

const (
	MinLenLogin = 3
)

type tokenService struct {
	config string
	secret string
}

type Service interface {
	GenerateToken(user domain.User, headers ...map[string]any) (domain.Token, error)
	ValidateToken(token string) (domain.User, error)
}

type customClaims struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

func (t *tokenService) GenerateToken(user domain.User, headers ...map[string]any) (domain.Token, error) {
	secret, err := createSecretKey(user.Username, t.secret)
	if err != nil {
		return "", errors.New("failed to create secret key")
	}

	duration, err := time.ParseDuration(t.config)
	if err != nil {
		return "", errors.New("invalid token duration config")
	}

	claims := &customClaims{
		Id:   string(user.Id),
		Name: user.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.Username,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	if len(headers) > 0 {
		for k, v := range headers[0] {
			token.Header[k] = v
		}
	}

	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", errors.New("failed to sign JWT")
	}

	return domain.Token(tokenString), nil
}

func (t *tokenService) ValidateToken(tokenString string) (domain.User, error) {

	token, err := jwt.ParseWithClaims(tokenString, &customClaims{}, func(token *jwt.Token) (interface{}, error) {
		claims, ok := token.Claims.(*customClaims)
		if !ok {
			return nil, errors.New("invalid token")
		}

		secret, err := createSecretKey(claims.Name, t.secret)
		if err != nil {
			return nil, err
		}

		return []byte(secret), nil
	})

	if err != nil {
		return domain.User{}, errors.New("failed to parse token")
	}

	claims, ok := token.Claims.(*customClaims)
	if !ok || !token.Valid {
		return domain.User{}, errors.New("invalid or expired token")
	}

	id, err := strconv.Atoi(claims.Id)
	if err != nil {
		return domain.User{}, errors.New("invalid user ID in token")
	}

	user := domain.User{
		Id:       id,
		Username: claims.Name,
	}

	return user, nil
}

func createSecretKey(username, secretBase string) (string, error) {

	if len(username) < MinLenLogin {
		return "", errors.New("username too short")
	}

	combined := secretBase + username

	hash := sha256.New()
	hash.Write([]byte(combined))

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func NewTokenService(config string, secret string) Service {
	return &tokenService{
		config: config,
		secret: secret,
	}
}
