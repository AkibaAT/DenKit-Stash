package auth

import (
	"denkit-stash/models"
	"fmt"
)

func CreateUser(db models.Database, username, role string) (*models.User, error) {
	existingUser, err := db.GetUserByUsername(username)
	if err == nil {
		return nil, fmt.Errorf("user '%s' already exists", existingUser.Username)
	}

	apiKey, err := GenerateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate API key: %v", err)
	}

	user := &models.User{
		Username:    username,
		DisplayName: username,
		APIKey:      apiKey,
		Role:        role,
		IsActive:    true,
	}

	err = db.CreateUser(user)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %v", err)
	}

	fmt.Printf("Created %s user: %s with API key: %s\n", role, username, apiKey)
	return user, nil
}

func EnsureUser(db models.Database, username, role, apiKey string) (*models.User, error) {
	if apiKey == "" {
		var err error
		apiKey, err = GenerateAPIKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate API key: %v", err)
		}
	}

	existingUser, err := db.GetUserByUsername(username)
	if err == nil {
		existingUser.DisplayName = username
		existingUser.APIKey = apiKey
		existingUser.Role = role
		existingUser.IsActive = true
		if err := db.UpdateUser(existingUser); err != nil {
			return nil, fmt.Errorf("failed to update user: %v", err)
		}

		fmt.Printf("Updated %s user: %s with configured API key\n", role, username)
		return existingUser, nil
	}

	user := &models.User{
		Username:    username,
		DisplayName: username,
		APIKey:      apiKey,
		Role:        role,
		IsActive:    true,
	}

	if err := db.CreateUser(user); err != nil {
		return nil, fmt.Errorf("failed to create user: %v", err)
	}

	fmt.Printf("Created %s user: %s with configured API key\n", role, username)
	return user, nil
}

func ListUsers(db models.Database) error {
	users, err := db.ListUsers()
	if err != nil {
		return fmt.Errorf("failed to list users: %v", err)
	}

	if len(users) == 0 {
		fmt.Println("No users found.")
		return nil
	}

	fmt.Printf("%-10s %-20s %-10s %-8s\n", "ID", "Username", "Role", "Active")
	fmt.Println("------------------------------------------------------")
	for _, user := range users {
		activeStr := "Yes"
		if !user.IsActive {
			activeStr = "No"
		}
		fmt.Printf("%-10d %-20s %-10s %-8s\n",
			user.ID, user.Username, user.Role, activeStr)
	}
	return nil
}

func DeactivateUser(db models.Database, username string) error {
	return setUserActive(db, username, false)
}

func ActivateUser(db models.Database, username string) error {
	return setUserActive(db, username, true)
}

func setUserActive(db models.Database, username string, active bool) error {
	user, err := db.GetUserByUsername(username)
	if err != nil {
		return fmt.Errorf("user '%s' not found", username)
	}

	state := "deactivated"
	action := "deactivate"
	if active {
		state = "active"
		action = "activate"
	}
	if user.IsActive == active {
		fmt.Printf("User '%s' is already %s.\n", username, state)
		return nil
	}

	user.IsActive = active
	err = db.UpdateUser(user)
	if err != nil {
		return fmt.Errorf("failed to %s user: %v", action, err)
	}

	fmt.Printf("User '%s' has been %s.\n", username, state)
	return nil
}
