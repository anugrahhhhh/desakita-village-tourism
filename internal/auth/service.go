// Package auth handles registration, login, the current viewer and account deletion.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/db"
	"desakita/internal/platform/form"
	"desakita/internal/platform/phone"
)

var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrLastManager        = errors.New("auth: user is the last manager of a village")
	ErrNotFound           = errors.New("auth: user not found")
)

type User struct {
	ID      int64
	Email   string
	Name    string
	Phone   string
	IsAdmin bool
}

func fromRow(u db.User) User {
	out := User{ID: u.ID, Email: u.Email, Name: u.Name, IsAdmin: u.IsAdmin}
	if u.Phone != nil {
		out.Phone = *u.Phone
	}
	return out
}

type Service struct {
	pool *pgxpool.Pool
	q    *db.Queries
	// dummyHash is verified against when the email is unknown, so login takes the same
	// time whether or not an account exists.
	dummyHash string
}

func NewService(pool *pgxpool.Pool) (*Service, error) {
	h, err := hashPassword("dummy-password-for-timing")
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, q: db.New(pool), dummyHash: h}, nil
}

type RegisterInput struct {
	Name, Email, Password, Phone string
}

func (in *RegisterInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.TrimSpace(in.Email)
	in.Phone = strings.TrimSpace(in.Phone)
}

func (in RegisterInput) validate() form.Errors {
	errs := form.Errors{}
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0:
		errs.Add("name", "validation.required")
	case n > 100:
		errs.Add("name", "validation.too_long")
	}
	switch {
	case in.Email == "":
		errs.Add("email", "validation.required")
	case len(in.Email) > 254:
		errs.Add("email", "validation.too_long")
	default:
		if addr, err := mail.ParseAddress(in.Email); err != nil || addr.Address != in.Email {
			errs.Add("email", "validation.email")
		}
	}
	if n := utf8.RuneCountInString(in.Password); n < 8 || n > 128 {
		errs.Add("password", "validation.password_length")
	}
	if in.Phone != "" && !phone.Valid(in.Phone) {
		errs.Add("phone", "validation.phone")
	}
	return errs
}

// Register creates an account. Invalid input returns *form.ValidationError.
func (s *Service) Register(ctx context.Context, in RegisterInput) (User, error) {
	in.normalize()
	if err := in.validate().Err(); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	var phone *string
	if in.Phone != "" {
		phone = &in.Phone
	}
	row, err := s.q.CreateUser(ctx, db.CreateUserParams{Email: in.Email, PasswordHash: hash, Name: in.Name, Phone: phone})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_key" {
			return User{}, form.Errors{"email": "auth.email_taken"}.Err()
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return fromRow(row), nil
}

// Authenticate returns the user for valid credentials, otherwise ErrInvalidCredentials.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	row, err := s.q.GetUserByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, pgx.ErrNoRows) {
		verifyPassword(password, s.dummyHash) //nolint:errcheck // timing equalisation only
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	ok, err := verifyPassword(password, row.PasswordHash)
	if err != nil {
		return User{}, err
	}
	if !ok {
		return User{}, ErrInvalidCredentials
	}
	return fromRow(row), nil
}

func (s *Service) UserByID(ctx context.Context, id int64) (User, error) {
	row, err := s.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return fromRow(row), nil
}

// PromoteAdmin grants admin rights to the account with email (CLI only).
func (s *Service) PromoteAdmin(ctx context.Context, email string) error {
	n, err := s.q.PromoteAdmin(ctx, strings.TrimSpace(email))
	if err != nil {
		return fmt.Errorf("promote admin: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAccount removes the user after re-checking the password. It refuses while the user
// is the last manager of any village. The user's bookings stay for the village's records
// but lose note and contact details.
func (s *Service) DeleteAccount(ctx context.Context, userID int64, password string) error {
	row, err := s.q.GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	ok, err := verifyPassword(password, row.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockMembershipsOfUserVillages(ctx, userID); err != nil {
			return fmt.Errorf("lock memberships: %w", err)
		}
		n, err := q.CountVillagesSolelyManagedBy(ctx, userID)
		if err != nil {
			return fmt.Errorf("count villages: %w", err)
		}
		if n > 0 {
			return ErrLastManager
		}
		if err := q.AnonymizeTravelerBookings(ctx, &userID); err != nil {
			return fmt.Errorf("anonymize bookings: %w", err)
		}
		if err := q.DeleteUser(ctx, userID); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	})
}
