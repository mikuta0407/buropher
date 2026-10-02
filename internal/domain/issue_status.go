package domain

// IssueStatus は issue_statuses 行。
type IssueStatus struct {
	ID               int64
	Name             string
	Description      *string
	IsClosed         bool
	Position         int
	DefaultDoneRatio *int
}

// DescriptionString は description（nil なら空文字列）。
func (s *IssueStatus) DescriptionString() string {
	if s.Description == nil {
		return ""
	}
	return *s.Description
}
