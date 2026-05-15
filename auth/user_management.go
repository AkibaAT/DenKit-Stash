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
	user, err := db.GetUserByUsername(username)
	if err != nil {
		return fmt.Errorf("user '%s' not found", username)
	}

	if !user.IsActive {
		fmt.Printf("User '%s' is already deactivated.\n", username)
		return nil
	}

	user.IsActive = false
	err = db.UpdateUser(user)
	if err != nil {
		return fmt.Errorf("failed to deactivate user: %v", err)
	}

	fmt.Printf("User '%s' has been deactivated.\n", username)
	return nil
}

func ActivateUser(db models.Database, username string) error {
	user, err := db.GetUserByUsername(username)
	if err != nil {
		return fmt.Errorf("user '%s' not found", username)
	}

	if user.IsActive {
		fmt.Printf("User '%s' is already active.\n", username)
		return nil
	}

	user.IsActive = true
	err = db.UpdateUser(user)
	if err != nil {
		return fmt.Errorf("failed to activate user: %v", err)
	}

	fmt.Printf("User '%s' has been activated.\n", username)
	return nil
}
