package auth

import (
	"context"
	"denkit-stash/models"
)

type contextKey string

const userKey contextKey = "user"

func SetUser(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, userKey, user)
}

func GetUser(ctx context.Context) (*models.User, bool) {
	user, ok := ctx.Value(userKey).(*models.User)
	return user, ok
}

func MustGetUser(ctx context.Context) *models.User {
	user, ok := GetUser(ctx)
	if !ok {
		panic("no authenticated user found in context")
	}
	return user
}
