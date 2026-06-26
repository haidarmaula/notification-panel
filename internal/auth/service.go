package auth

type AuthService struct {
	repo *AuthRepository
}

func NewAuthService(repo *AuthRepository) *AuthService {
	return &AuthService{
		repo: repo,
	}
}

func (s *AuthService) Login(email, password string) (*User, bool) {
	user, found := s.repo.FindByEmail(email)
	if !found || user.Password != password {
		return nil, false
	}
	return user, true
}

func (s *AuthService) GetUserByID(id int64) (*User, bool) {
	return s.repo.FindByID(id)
}
