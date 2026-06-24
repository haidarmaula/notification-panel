package auth

type AuthService struct {
	repo *AuthRepository
}

func NewAuthService(repo *AuthRepository) *AuthService {
	return &AuthService{
		repo: repo,
	}
}

func (s *AuthService) GetUser(email, password string) (*User, bool) {
	return s.repo.FindUser(email, password)
}
