package notifications

type NotificationService struct {
	repo   *NotificationRepository
	nextID int64
}

func NewNotificationService(repo *NotificationRepository) *NotificationService {
	return &NotificationService{
		repo:   repo,
		nextID: 1,
	}
}

func (s *NotificationService) GetAll() []Notification {
	return s.repo.FindAll()
}

func (s *NotificationService) GetByID(id int64) (*Notification, bool) {
	return s.repo.FindByID(id)
}

func (s *NotificationService) Create(title, message string) Notification {
	n := Notification{
		ID:      s.nextID,
		Title:   title,
		Message: message,
	}

	s.nextID++
	return s.repo.Create(n)
}

func (s *NotificationService) Delete(id int64) bool {
	return s.repo.Delete(id)
}
