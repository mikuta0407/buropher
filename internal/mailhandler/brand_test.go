package mailhandler

import (
	"strconv"
	"strings"
	"testing"
)

// buropher 拡張: mail.message_id_prefix = "buropher" で送ったメールへの返信（In-Reply-To が buropher.*）も
// Redmine の redmine.* と同じく受け付ける。
func TestReplyToBuropherMessageID(t *testing.T) {
	c := setup(t)
	pid, err := c.d.InsertReturningID(c.ctx, `INSERT INTO issue_journals (issue_id, user_id, notes, private_notes, created_at, updated_at) VALUES (1, 2, 'Notes', ?, ?, ?)`,
		false, dbNow(), dbNow())
	c.must(err)
	j := c.assertJournal(c.submit("ticket_reply.eml", Options{}, replaceInReplyTo("<buropher.journal-"+strconv.FormatInt(pid, 10)+".20060719210421@osiris>")))
	if !strings.Contains(j.Notes, "This is reply") {
		t.Errorf("journal = %q", j.Notes)
	}
}

func TestMessageIDPrefixes(t *testing.T) {
	for _, s := range []string{"<redmine.issue-1.20060719210421@osiris>", "<buropher.issue-1.20060719210421.2@osiris>"} {
		m := messageIDRe.FindStringSubmatch(s)
		if m == nil || m[1] != "issue" || m[2] != "1" {
			t.Errorf("%s: %v", s, m)
		}
	}
	if messageIDRe.MatchString("<other.issue-1.20060719210421@osiris>") {
		t.Error("other prefix matched")
	}
}
