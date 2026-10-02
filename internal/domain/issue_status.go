package domain

// IssueStatus は issue_statuses 行（Redmine IssueStatus）。
type IssueStatus struct {
	ID               int64
	Name             string
	Description      string
	IsClosed         bool
	Position         int
	DefaultDoneRatio *int
}

// String は IssueStatus#to_s。
func (s *IssueStatus) String() string { return s.Name }
