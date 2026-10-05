# frozen_string_literal: true
#
# Redmine 7.0.1 の Mailer が生成するメールを JSON に書き出す（buropher のメール互換テストの期待値）。
# usage: internal/server/testdata/mail/gen/regen.sh
#
# 各ケースはメールの配列（受信者ごとに 1 通）を出力する。Date ヘッダと multipart の boundary は出力しない。
require 'json'

ActiveJob::Base.queue_adapter = :inline
ActionMailer::Base.delivery_method = :test
ActionMailer::Base.perform_deliveries = true
ActionMailer::Base.raise_delivery_errors = true

$out = []

DUMP_HEADERS = %w(From To Cc Bcc Subject Message-ID In-Reply-To References List-Id Auto-Submitted
                  X-Mailer X-Auto-Response-Suppress Mime-Version MIME-Version Content-Type).freeze

def dump_mail(name, m)
  headers = {}
  m.header.fields.each do |f|
    n = f.name
    next unless n.start_with?('X-') || DUMP_HEADERS.include?(n)
    v = f.value.to_s
    v = v.sub(/boundary="?[^";]+"?/, 'boundary=BOUNDARY') if n == 'Content-Type'
    headers[n] = v
  end
  text = nil
  html = nil
  if m.multipart?
    text = m.text_part&.decoded
    html = m.html_part&.decoded
  elsif m.mime_type == 'text/html'
    html = m.decoded
  else
    text = m.decoded
  end
  {
    'case' => name,
    'to' => Array(m.to), 'cc' => Array(m.cc), 'bcc' => Array(m.bcc),
    'subject' => m.subject,
    'headers' => headers,
    'header_order' => m.header.fields.map(&:name),
    'text' => text, 'html' => html,
    'text_cte' => (m.multipart? ? m.text_part&.content_transfer_encoding : m.content_transfer_encoding),
  }
end

def kase(name)
  ActionMailer::Base.deliveries.clear
  User.current = nil
  yield
  mails = ActionMailer::Base.deliveries.map {|m| dump_mail(name, m)}
  $out << {'case' => name, 'mails' => mails}
ensure
  User.current = nil
end

def with_settings(h)
  saved = {}
  h.each do |k, v|
    saved[k] = Setting.send(k)
    Setting.send("#{k}=", v)
  end
  yield
ensure
  saved.each {|k, v| Setting.send("#{k}=", v)}
end

def fixed_token(user, action, value)
  t = Token.create!(:user => user, :action => action)
  t.update_column(:value, value)
  t.reload
end

# ---- チケット
[1, 2, 3, 4, 6, 14].each do |id|
  kase("issue_add_#{id}") { Mailer.deliver_issue_add(Issue.find(id)) }
end
[1, 2, 3, 4, 5].each do |id|
  kase("issue_edit_#{id}") { Mailer.deliver_issue_edit(Journal.find(id)) }
end
kase('issue_add_1_plain') { with_settings(plain_text_mail: '1') { Mailer.deliver_issue_add(Issue.find(1)) } }
kase('issue_add_1_nostatus') do
  with_settings(show_status_changes_in_mail_subject: '0') { Mailer.deliver_issue_add(Issue.find(1)) }
end
kase('issue_add_1_header_footer') do
  with_settings(emails_header: "*Header* text", emails_footer: "Footer _line_\nsecond", text_formatting: 'textile') do
    Mailer.deliver_issue_add(Issue.find(1))
  end
end
kase('issue_add_1_mail_from_name') do
  with_settings(mail_from: 'Redmine app <redmine@somenet.foo>', host_name: 'mydomain.foo/rdm', protocol: 'https') do
    Mailer.deliver_issue_add(Issue.find(1))
  end
end
kase('issue_edit_1_ja') do
  u = User.find(2)
  lang = u.language
  tz = u.pref.time_zone
  u.update_column(:language, 'ja')
  begin
    Mailer.deliver_issue_edit(Journal.find(1))
    Mailer.issue_edit(u, Journal.find(2)).deliver_now
  ensure
    u.update_column(:language, lang)
  end
end

# ---- ニュース・文書・添付・フォーラム・Wiki
kase('news_added_1') { Mailer.deliver_news_added(News.find(1)) }
kase('news_comment_added_1') { Mailer.deliver_news_comment_added(Comment.find(1)) }
kase('document_added_1') { Mailer.deliver_document_added(Document.find(1), User.find(2)) }
kase('attachments_added_project') { Mailer.deliver_attachments_added([Attachment.find(8), Attachment.find(22)]) }
kase('attachments_added_version') { Mailer.deliver_attachments_added([Attachment.find(9)]) }
kase('attachments_added_document') { Mailer.deliver_attachments_added([Attachment.find(2)]) }
kase('message_posted_1') { Mailer.deliver_message_posted(Message.find(1)) }
kase('message_posted_2') { Mailer.deliver_message_posted(Message.find(2)) }
kase('wiki_content_added_1') { Mailer.deliver_wiki_content_added(WikiContent.find(1)) }
kase('wiki_content_updated_1') { Mailer.deliver_wiki_content_updated(WikiContent.find(1)) }

# ---- アカウント・セキュリティ
kase('account_information') { Mailer.deliver_account_information(User.find(2), 'pAsSwoRd') }
kase('account_information_nopassword') { Mailer.deliver_account_information(User.find(3), nil) }
kase('account_activation_request') { Mailer.deliver_account_activation_request(User.find(3)) }
kase('account_activated') { Mailer.deliver_account_activated(User.find(3)) }
kase('lost_password') do
  t = fixed_token(User.find(2), 'recovery', 'a' * 64)
  Mailer.deliver_lost_password(User.find(2), t)
  Mailer.deliver_lost_password(User.find(2), t, 'other@example.net')
end
kase('register') do
  t = fixed_token(User.find(3), 'register', 'b' * 64)
  Mailer.deliver_register(User.find(3), t)
end
kase('security_notification_password') do
  sender = User.find(2)
  sender.remote_ip = '10.1.2.3'
  Mailer.deliver_password_updated(User.find(2), sender)
end
kase('security_notification_mail_added') do
  sender = User.find(1)
  sender.remote_ip = '127.0.0.1'
  Mailer.deliver_security_notification(User.find(2), sender,
    message: :mail_body_security_notification_add, field: :field_mail, value: 'new@example.net',
    recipients: ['old@example.net'])
end
kase('security_notification_title_only') do
  sender = User.find(2)
  sender.remote_ip = '127.0.0.1'
  Mailer.deliver_security_notification(User.find(2), sender,
    message: :mail_body_security_notification_notify_enabled, value: 'jsmith@somenet.foo',
    title: :label_my_account)
end
kase('settings_updated') do
  sender = User.find(1)
  sender.remote_ip = '127.0.0.1'
  Mailer.deliver_settings_updated(sender, [:host_name, :login_required])
end
kase('test_email') { Mailer.deliver_test_email(User.find(1)) }
kase('reminders_7') { Mailer.reminders(:days => 7) }
kase('reminders_42') { Mailer.reminders(:days => 42) }
kase('reminders_42_user3') { Mailer.reminders(:days => 42, :users => ['3']) }

# ---- 変更を伴うケース（最後に実行）: チケットを更新してコールバック経由で配信する
kase('e2e_issue_update') do
  User.current = User.find(2)
  issue = Issue.find(1)
  issue.init_journal(User.find(2), "Changed the status\n\nwith *notes*")
  issue.status_id = 2
  issue.save!
end

File.write(ARGV[0] || 'mails.json', JSON.pretty_generate($out) + "\n")
