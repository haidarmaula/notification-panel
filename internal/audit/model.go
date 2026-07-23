package audit

type LogParams struct {
	Action     string
	EntityType string
	EntityName string
	EntityID   int64
	Before     any
	After      any
}
