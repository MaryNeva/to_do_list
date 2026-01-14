package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"to-do-list.com/users/pkg/domain"
)

var tableUsers = "users"

type usersStorePostgres struct {
	db *pgxpool.Pool
}

func (u *usersStorePostgres) GetUser(ctx context.Context, id int) (user domain.User, err error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE id = $1;", tableUsers)

	rows, err := u.db.Query(ctx, query, id)
	if err != nil {
		return domain.User{}, err
	}

	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[UserModel])
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, err
	}

	if err != nil {
		return domain.User{}, err
	}

	return toUserDomain(model), err
}

func (u *usersStorePostgres) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	model := toUserModel(user)

	query := fmt.Sprintf("INSERT INTO %s (username, email, password) VALUES ($1) RETURNING id", tableUsers)

	err := u.db.QueryRow(ctx, query, model.Username, model.Password).Scan(&model.Id)
	if err != nil {
		return domain.User{}, err
	}

	return toUserDomain(model), nil
}

func (u *usersStorePostgres) UpdateUser(ctx context.Context, user domain.User) error {
	query := fmt.Sprintf("UPDATE %s SET username = $1, email = $2, password = $3", tableUsers)
	_, err := u.db.Exec(ctx, query, user.Username, user.Email, user.Password)
	if err != nil {
		return err
	}

	return err
}

func (u *usersStorePostgres) DeleteUser(ctx context.Context, id int) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1;", tableUsers)
	_, err := u.db.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	return err
}

func NewUserStore(ctx context.Context, dbPostgres *pgxpool.Pool) (store domain.UserStore, err error) {
	return &usersStorePostgres{db: dbPostgres}, nil
}
