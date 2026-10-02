package mailhandler

// test/unit/mail_handler_test.rb の移植（文字コード・添付・本文の取り出し・区切り）。

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/domain"
)

func (c *tc) diskfile(a *domain.Attachment) []byte {
	c.t.Helper()
	b, err := os.ReadFile(c.h.Attachments.Diskfile(a))
	c.must(err)
	return b
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (c *tc) assertSingleAttachment(name string, filename string, size int64, digest string) *domain.Attachment {
	c.t.Helper()
	iss := c.assertIssue(c.submit(name, Options{Issue: map[string]string{"project": "ecookbook"}}))
	as := c.attachments("issue", iss.ID)
	if len(as) != 1 {
		c.t.Fatalf("attachments = %d", len(as))
	}
	a := as[0]
	if a.Filename != filename {
		c.t.Errorf("filename = %q, want %q", a.Filename, filename)
	}
	if a.Filesize != size {
		c.t.Errorf("filesize = %d", a.Filesize)
	}
	data := c.diskfile(a)
	if int64(len(data)) != size {
		c.t.Errorf("file size = %d", len(data))
	}
	if digest != "" {
		if a.Digest != digest {
			c.t.Errorf("digest = %s", a.Digest)
		}
		if sha(data) != digest {
			c.t.Errorf("file digest = %s", sha(data))
		}
	}
	return a
}

func TestAddIssueFromAppleMail(t *testing.T) {
	c := setup(t)
	c.assertSingleAttachment("apple_mail_with_attachment.eml", "paella.jpg", 10790,
		"4474dd534c36bdd212e2efc549507377c3e77147c9167b66dedcebfe9da8807f")
}

func TestThunderbirdWithAttachmentJa(t *testing.T) {
	c := setup(t)
	c.assertSingleAttachment("thunderbird_with_attachment_ja.eml", "テスト.txt", 5,
		"f2ca1bb6c7e907d06dafe4687e579fce76b37e4e93b7605022da52e6ccc26fd2")
}

func TestInvalidUTF8(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("invalid_utf8.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if d := ptrVal(iss.Description); d != "Здравствуйте?" {
		t.Errorf("description = %q", d)
	}
}

func TestGmailWithAttachmentJa(t *testing.T) {
	c := setup(t)
	c.assertSingleAttachment("gmail_with_attachment_ja.eml", "テスト.txt", 5,
		"f2ca1bb6c7e907d06dafe4687e579fce76b37e4e93b7605022da52e6ccc26fd2")
}

func TestThunderbirdWithAttachmentLatin1(t *testing.T) {
	c := setup(t)
	c.assertSingleAttachment("thunderbird_with_attachment_iso-8859-1.eml", strings.Repeat("ÄäÖöÜü", 11)+".png", 130,
		"5635d67364de20432247e651dfe86fcb2265ad5e9750bd8bba7319a86363e738")
}

func TestGmailWithAttachmentLatin1(t *testing.T) {
	c := setup(t)
	c.assertSingleAttachment("gmail_with_attachment_iso-8859-1.eml", strings.Repeat("ÄäÖöÜü", 11)+".txt", 5,
		"f2ca1bb6c7e907d06dafe4687e579fce76b37e4e93b7605022da52e6ccc26fd2")
}

func TestMailWithAttachmentLatin2(t *testing.T) {
	c := setup(t)
	a := c.assertSingleAttachment("ticket_with_text_attachment_iso-8859-2.eml", "latin2.txt", 19, "")
	if got := string(c.diskfile(a)); got != "p\xF8\xEDli\xB9 \xBEluou\xE8k\xFD k\xF9n" {
		t.Errorf("content = %q", got)
	}
}

func TestEmptyAttachmentShouldNotBeImported(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_with_empty_attachment.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if n := len(c.attachments("issue", iss.ID)); n != 0 {
		t.Errorf("attachments = %d", n)
	}
}

func TestMultipleInlineTextPartsShouldBeAppendedToIssueDescription(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("multiple_text_parts.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	d := ptrVal(iss.Description).(string)
	for _, s := range []string{"first", "second", "third"} {
		if !strings.Contains(d, s) {
			t.Errorf("description %q does not contain %q", d, s)
		}
	}
}

func TestEmptyTextPartShouldNotStopLookingForContent(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("empty_text_part.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if d := ptrVal(iss.Description); d != "The html part." {
		t.Errorf("description = %q", d)
	}
}

func TestEmptyTextAndHTMLPartShouldMakeAnEmptyDescription(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("empty_text_and_html_part.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if d := ptrVal(iss.Description); d != "" && d != nil {
		t.Errorf("description = %q", d)
	}
}

func TestPreferredBodyPartSetting(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_preferred_body_part", "plain")
	iss := c.assertIssue(c.submit("different_contents_in_text_and_html.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if d := ptrVal(iss.Description); d != "The text part." {
		t.Errorf("plain: description = %q", d)
	}
	c.set("mail_handler_preferred_body_part", "html")
	iss = c.assertIssue(c.submit("different_contents_in_text_and_html.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if d := ptrVal(iss.Description); d != "The html part." {
		t.Errorf("html: description = %q", d)
	}
}

func TestAttachmentTextPartShouldBeAddedAsIssueAttachment(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("multiple_text_parts.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if strings.Contains(ptrVal(iss.Description).(string), "Plain text attachment") {
		t.Error("attachment text in description")
	}
	var found *domain.Attachment
	for _, a := range c.attachments("issue", iss.ID) {
		if a.Filename == "textfile.txt" {
			found = a
		}
	}
	if found == nil {
		t.Fatal("textfile.txt not attached")
	}
	if !strings.Contains(string(c.diskfile(found)), "Plain text attachment") {
		t.Error("attachment content")
	}
}

func (c *tc) subjectOf(name string, o Options) string {
	c.t.Helper()
	return c.assertIssue(c.submit(name, o)).Subject
}

func TestAddIssueWithISO88591Subject(t *testing.T) {
	c := setup(t)
	if s := c.subjectOf("subject_as_iso-8859-1.eml", Options{Issue: map[string]string{"project": "ecookbook"}}); s != "Testmail from Webmail: ä ö ü..." {
		t.Errorf("subject = %q", s)
	}
}

func TestQuotedPrintableUTF8(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("quoted_printable_utf8.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if d := ptrVal(iss.Description); d != "Freundliche Grüsse" {
		t.Errorf("description = %q", d)
	}
}

func TestGmailISO88592(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("gmail-iso8859-2.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if d := ptrVal(iss.Description).(string); !strings.Contains(d, "Na štriku se suši šosić.") {
		t.Errorf("description = %q", d)
	}
}

func TestAddIssueWithJapaneseSubject(t *testing.T) {
	c := setup(t)
	if s := c.subjectOf("subject_japanese_1.eml", Options{Issue: map[string]string{"project": "ecookbook"}}); s != "テスト" {
		t.Errorf("subject = %q", s)
	}
}

func TestAddIssueWithKoreanBody(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("body_ks_c_5601-1987.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if d := ptrVal(iss.Description); d != "고맙습니다." {
		t.Errorf("description = %q", d)
	}
}

func TestAddIssueWithNoSubjectHeader(t *testing.T) {
	c := setup(t)
	c.set("default_language", "en")
	if s := c.subjectOf("no_subject_header.eml", Options{Issue: map[string]string{"project": "ecookbook"}}); s != "(no subject)" {
		t.Errorf("subject = %q", s)
	}
}

func TestAddIssueWithMixedJapaneseSubject(t *testing.T) {
	c := setup(t)
	if s := c.subjectOf("subject_japanese_2.eml", Options{Issue: map[string]string{"project": "ecookbook"}}); s != "Re: テスト" {
		t.Errorf("subject = %q", s)
	}
}

func TestAddIssueWithISO2022JPMSSubject(t *testing.T) {
	c := setup(t)
	if s := c.subjectOf("subject_japanese_3.eml", Options{Issue: map[string]string{"project": "ecookbook"}}); !strings.Contains(s, "丸数字テスト") {
		t.Errorf("subject = %q", s)
	}
}

func TestShouldConvertTagsOfHTMLOnlyEmails(t *testing.T) {
	c := setup(t)
	c.set("text_formatting", "textile")
	iss := c.assertIssue(c.submit("ticket_html_only.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if iss.Subject != "HTML email" {
		t.Errorf("subject = %q", iss.Subject)
	}
	if d := ptrVal(iss.Description); d != "This is a *html-only* email.\r\n\r\nh1. With a title\r\n\r\nand a paragraph." {
		t.Errorf("description = %q", d)
	}
}

func TestShouldHandleOutlookWebAccess2010HTMLOnly(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("outlook_web_access_2010_html_only.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if iss.Subject != "Upgrade Redmine to 3.0.x" {
		t.Errorf("subject = %q", iss.Subject)
	}
	if d := ptrVal(iss.Description); d != "A mess.\r\n\r\n--Geoff Maciolek\r\nMYCOMPANYNAME, LLC" {
		t.Errorf("description = %q", d)
	}
}

func TestShouldHandleOutlook2010HTMLOnly(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("outlook_2010_html_only.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if iss.Subject != "Test email" {
		t.Errorf("subject = %q", iss.Subject)
	}
	want := "Simple, unadorned test email generated by Outlook 2010. It is in HTML format, but" +
		" no special formatting has been chosen. I’m going to save this as a draft and then manually" +
		" drop it into the Inbox for scraping by Redmine 3.0.2."
	if d := ptrVal(iss.Description); d != want {
		t.Errorf("description = %q", d)
	}
}

func TestTruncateEmailsWithNoSetting(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_body_delimiters", "")
	d := ptrVal(c.assertIssue(c.submit("ticket_on_given_project.eml", Options{})).Description).(string)
	if !strings.Contains(d, "---") || !strings.Contains(d, "This paragraph is after the delimiter") {
		t.Errorf("description = %q", d)
	}
}

func TestTruncateEmailsWithASingleString(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_body_delimiters", "---")
	d := ptrVal(c.assertIssue(c.submit("ticket_on_given_project.eml", Options{})).Description).(string)
	if !strings.Contains(d, "This paragraph is before delimiters") || !strings.Contains(d, "--- This line starts with a delimiter") {
		t.Errorf("description = %q", d)
	}
	if regexp.MustCompile(`(?m)^---\x{00A0}$`).MatchString(d) || strings.Contains(d, "This paragraph is after the delimiter") {
		t.Errorf("not truncated: %q", d)
	}
}

func TestTruncateEmailsWithASingleQuotedReply(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_body_delimiters", "--- Reply above. Do not remove this line. ---")
	j := c.assertJournal(c.submit("issue_update_with_quoted_reply_above.eml", Options{}))
	if !strings.Contains(j.Notes, "An update to the issue by the sender.") {
		t.Errorf("notes = %q", j.Notes)
	}
	if strings.Contains(j.Notes, "--- Reply above. Do not remove this line. ---") || strings.Contains(j.Notes, "Looks like the JSON api for projects was missed.") {
		t.Errorf("not truncated: %q", j.Notes)
	}
}

func TestTruncateEmailsWithMultipleQuotedReplies(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_body_delimiters", "--- Reply above. Do not remove this line. ---")
	j := c.assertJournal(c.submit("issue_update_with_multiple_quoted_reply_above.eml", Options{}))
	if !strings.Contains(j.Notes, "An update to the issue by the sender.") {
		t.Errorf("notes = %q", j.Notes)
	}
	if strings.Contains(j.Notes, "--- Reply above. Do not remove this line. ---") || strings.Contains(j.Notes, "Looks like the JSON api for projects was missed.") {
		t.Errorf("not truncated: %q", j.Notes)
	}
}

func TestTruncateEmailsWithMultipleStrings(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_body_delimiters", "---\nBREAK")
	d := ptrVal(c.assertIssue(c.submit("ticket_on_given_project.eml", Options{})).Description).(string)
	if !strings.Contains(d, "This paragraph is before delimiters") {
		t.Errorf("description = %q", d)
	}
	if strings.Contains(d, "BREAK") || strings.Contains(d, "This paragraph is between delimiters") ||
		regexp.MustCompile(`(?m)^---$`).MatchString(strings.ReplaceAll(d, "\r", "")) || strings.Contains(d, "This paragraph is after the delimiter") {
		t.Errorf("not truncated: %q", d)
	}
}

func TestTruncateEmailsUsingARegexDelimiter(t *testing.T) {
	c := setup(t)
	delimiter := "On .*, .* at .*, .* <.*<mailto:.*>> wrote:"
	c.set("mail_handler_enable_regex_delimiters", "1")
	c.set("mail_handler_body_delimiters", delimiter)
	d := ptrVal(c.assertIssue(c.submit("ticket_reply_from_mail.eml", Options{})).Description).(string)
	if !strings.Contains(d, "This paragraph is before delimiter") ||
		strings.Contains(d, "On Wed, 11 Oct at 1:05 PM, Jon Smith <jsmith@somenet.foo<mailto:jsmith@somenet.foo>> wrote:") ||
		strings.Contains(d, "This paragraph is after the delimiter") {
		t.Errorf("regex: description = %q", d)
	}
	c.set("mail_handler_enable_regex_delimiters", "0")
	d = ptrVal(c.assertIssue(c.submit("ticket_reply_from_mail.eml", Options{})).Description).(string)
	if !strings.Contains(d, "This paragraph is before delimiter") ||
		!strings.Contains(d, "On Wed, 11 Oct at 1:05 PM, Jon Smith <jsmith@somenet.foo<mailto:jsmith@somenet.foo>> wrote:") ||
		!strings.Contains(d, "This paragraph is after the delimiter") {
		t.Errorf("plain: description = %q", d)
	}
}

func TestAttachmentsThatMatchMailHandlerExcludedFilenamesShouldBeIgnored(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_excluded_filenames", "*.vcf,\n *.jpg")
	iss := c.assertIssue(c.submit("ticket_with_attachment.eml", Options{Issue: map[string]string{"project": "onlinestore"}}))
	if n := len(c.attachments("issue", iss.ID)); n != 0 {
		t.Errorf("attachments = %d", n)
	}
}

func TestAttachmentsThatMatchMailHandlerExcludedFilenamesByRegexShouldBeIgnored(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_excluded_filenames", `.+\.vcf,(pa|nut)ella\.jpg`)
	c.set("mail_handler_enable_regex_excluded_filenames", "1")
	iss := c.assertIssue(c.submit("ticket_with_attachment.eml", Options{Issue: map[string]string{"project": "onlinestore"}}))
	if n := len(c.attachments("issue", iss.ID)); n != 0 {
		t.Errorf("attachments = %d", n)
	}
}

func TestAttachmentsThatDoNotMatchMailHandlerExcludedFilenamesShouldBeAttached(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_excluded_filenames", "*.vcf, *.gif")
	iss := c.assertIssue(c.submit("ticket_with_attachment.eml", Options{Issue: map[string]string{"project": "onlinestore"}}))
	if n := len(c.attachments("issue", iss.ID)); n != 1 {
		t.Errorf("attachments = %d", n)
	}
}

func TestEmailWithLongSubjectLine(t *testing.T) {
	c := setup(t)
	s := c.subjectOf("ticket_with_long_subject.eml", Options{})
	str := "New ticket on a given project with a very long subject line" +
		" which exceeds 255 chars and should not be ignored but chopped off." +
		" And if the subject line is still not long enough, we just add more text." +
		" And more text. Wow, this is really annoying." +
		" Especially, if you have nothing to say..."
	if s != str[:255] {
		t.Errorf("subject = %q", s)
	}
}

func TestFirstKeywordShouldBeMatched(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_with_duplicate_keyword.eml", Options{AllowOverride: []string{"priority"}}))
	if iss.PriorityID != c.idByName("issue_priorities", "High") {
		t.Errorf("priority = %d", iss.PriorityID)
	}
}

func TestKeywordAfterDelimiterShouldBeIgnored(t *testing.T) {
	c := setup(t)
	c.set("mail_handler_body_delimiters", "== DELIMITER ==")
	iss := c.assertIssue(c.submit("ticket_with_keyword_after_delimiter.eml", Options{AllowOverride: []string{"priority"}}))
	if iss.PriorityID != c.idByName("issue_priorities", "Normal") {
		t.Errorf("priority = %d", iss.PriorityID)
	}
}

func TestSmimeSignature(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("smime_signature.eml", Options{Issue: map[string]string{"project": "onlinestore"}}))
	if iss.Subject != "Self-Signed S/MIME signature" || iss.AuthorID != 2 || iss.ProjectID != 2 {
		t.Errorf("subject/author/project = %q %d %d", iss.Subject, iss.AuthorID, iss.ProjectID)
	}
	if d := ptrVal(iss.Description); d != "smime.sh.txt describes how to create Self-Signed S/MIME Certs." {
		t.Errorf("description = %q", d)
	}
	as := c.attachments("issue", iss.ID)
	if len(as) != 2 {
		t.Fatalf("attachments = %d", len(as))
	}
	if as[0].Filename != "smime.sh.txt" || as[0].ContentType != "text/plain" {
		t.Errorf("attachment 0 = %q %q", as[0].Filename, as[0].ContentType)
	}
	if as[1].Filename != "smime.p7s" || as[1].ContentType != "application/x-pkcs7-signature" {
		t.Errorf("attachment 1 = %q %q", as[1].Filename, as[1].ContentType)
	}
}
