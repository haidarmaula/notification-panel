package auth

type AuthRepository struct {
	users []User
}

func NewAuthRepository() *AuthRepository {
	return &AuthRepository{
		users: []User{
			{
				ID:       1,
				Email:    "admin@gmail.com",
				Password: "admin",
			},
		},
	}
}

func (r *AuthRepository) FindByID(id int64) (*User, bool) {
	for i := range r.users {
		if r.users[i].ID == id {
			return &r.users[i], true
		}
	}
	return nil, false
}

func (r *AuthRepository) FindByEmail(email string) (*User, bool) {
	for i := range r.users {
		if r.users[i].Email == email {
			return &r.users[i], true
		}
	}
	return nil, false
}
