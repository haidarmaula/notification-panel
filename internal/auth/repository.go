package auth

type AuthRepository struct {
	users []User
}

func NewAuthRepository() *AuthRepository {
	return &AuthRepository{
		users: []User{
			{
				Email:    "admin@gmail.com",
				Password: "admin",
			},
		},
	}
}

func (r *AuthRepository) FindUser(email, password string) (*User, bool) {
	for i := range r.users {
		if r.users[i].Email == email && r.users[i].Password == password {
			return &r.users[i], true
		}
	}
	return nil, false
}

// func (r *AuthRepository) Create() User {
//
// }
