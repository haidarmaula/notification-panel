package notifications

type NotificationRepository struct {
	data []Notification
}

func NewNotificationRepository() *NotificationRepository {
	return &NotificationRepository{
		data: []Notification{},
	}
}

func (r *NotificationRepository) FindAll() []Notification {
	return r.data
}

func (r *NotificationRepository) FindByID(id int64) (*Notification, bool) {
	for _, n := range r.data {
		if n.ID == id {
			return &n, true
		}
	}
	return nil, false
}

func (r *NotificationRepository) Create(n Notification) Notification {
	r.data = append(r.data, n)
	return n
}

func (r *NotificationRepository) Delete(id int64) bool {
	for i, n := range r.data {
		if n.ID == id {
			r.data = append(r.data[:i], r.data[i+1:]...)
			return true
		}
	}
	return false
}
