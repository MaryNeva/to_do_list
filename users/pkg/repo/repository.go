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

func (u *usersStorePostgres) LoginUser(ctx context.Context, username string) (domain.User, error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE username = $1;", tableUsers)

	rows, err := u.db.Query(ctx, query, username)
	if err != nil {
		return domain.User{}, errors.New("failed to execute query")
	}

	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[UserModel])
	if err != nil {
		return domain.User{}, errors.New("not found username or password in database")
	}

	return toUserDomain(model), nil
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

func (u *usersStorePostgres) GetList(ctx context.Context) (users []domain.User, err error) {
	query := fmt.Sprintf("SELECT * FROM %s;", tableUsers)

	rows, err := u.db.Query(ctx, query)
	if err != nil {
		return []domain.User{}, errors.New("failed to parse users rows")
	}

	defer rows.Close()

	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[UserModel])
	if errors.Is(err, pgx.ErrNoRows) {
		return []domain.User{}, err
	}

	if err != nil {
		return []domain.User{}, err
	}

	if len(models) == 0 {
		return users, errors.New("no merchants found")
	}

	users = make([]domain.User, len(models))
	for i, model := range models {
		users[i] = toUserDomain(model)
	}

	return users, nil
}

func (u *usersStorePostgres) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	model := toUserModel(user)

	query := fmt.Sprintf("INSERT INTO %s (username, email, password) VALUES ($1, $2, $3) RETURNING id, created_at", tableUsers)
	err := u.db.QueryRow(ctx, query, model.Username, model.Email, model.Password).Scan(&model.Id, &model.CreatedAt)
	if err != nil {
		return domain.User{}, err
	}

	return toUserDomain(model), nil
}

func (u *usersStorePostgres) UpdateUser(ctx context.Context, id int, user domain.User) error {
	query := fmt.Sprintf("UPDATE %s SET username = $1, email = $2, password = $3 WHERE id=$4", tableUsers)
	_, err := u.db.Exec(ctx, query, user.Username, user.Email, user.Password, id)
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

func NewUserStore(dbPostgres *pgxpool.Pool) domain.UserStore {
	return &usersStorePostgres{db: dbPostgres}
}
