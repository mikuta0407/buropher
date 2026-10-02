# エクスポートの結合テスト用に、Redmine DB(必ずコピー)へ多様なデータを投入する。
# 添付ファイル・暗号化列・圧縮 Wiki 版・各種 YAML 列・ポリモーフィック行などを作る。
#
# 使い方:
#   cd <redmine> && DATABASE_URL=sqlite3:<copy.sqlite3> SECRET_KEY_BASE=x RAILS_ENV=production \
#     POPULATE_FILES=<files dir> POPULATE_CIPHER_KEY=<key> bin/rails runner <this file>
# 前提: redmine:load_default_data 済み(サンプル DB はそのまま使える)。
require 'stringio'

files_dir = ENV.fetch('POPULATE_FILES')
FileUtils.mkdir_p(files_dir)
Attachment.storage_path = files_dir
Redmine::Configuration['x']
Redmine::Configuration.instance_variable_get(:@config)['database_cipher_key'] = ENV.fetch('POPULATE_CIPHER_KEY')

class UpFile < StringIO
  attr_reader :original_filename, :content_type
  def initialize(data, name, ctype)
    super(data)
    @original_filename = name
    @content_type = ctype
  end
end

ActiveRecord::Base.transaction do
  admin = User.find_by(login: 'admin') || User.where(admin: true).first
  User.current = admin

  # 設定(serialized / 非 serialized)
  Setting.app_title = 'Export テスト'
  Setting.wiki_compression = 'gzip'
  Setting.default_language = 'ja'
  Setting.issue_list_default_columns = %w[tracker status priority subject assigned_to updated_on]
  Setting.mail_handler_excluded_filenames = ''
  Setting.per_page_options = '25,50,100'
  Setting.enabled_scm = %w[Subversion Git]
  Setting.cross_project_issue_relations = '1'

  # ユーザー・グループ
  u1 = User.new(firstname: '太郎', lastname: '山田', mail: 'taro@example.com', language: 'ja')
  u1.login = 'taro'
  u1.password = u1.password_confirmation = 'password123'
  u1.twofa_scheme = 'totp'
  u1.twofa_totp_key = 'JBSWY3DPEHPK3PXP'
  u1.save!
  u1.pref.others = { no_self_notified: '1', my_page_layout: { 'top' => ['issuesassignedtome'] },
                     recently_used_project_ids: '1', bookmarked_project_ids: '1', gantt_zoom: 2 }
  u1.pref.time_zone = 'Tokyo'
  u1.pref.save!
  u2 = User.new(firstname: 'Locked', lastname: 'User', mail: 'locked@example.com')
  u2.login = 'locked'
  u2.password = u2.password_confirmation = 'password123'
  u2.status = User::STATUS_LOCKED
  u2.save!
  g = Group.create!(lastname: '開発チーム')
  g.users << u1
  Token.create!(user: u1, action: 'api')

  ldap = AuthSourceLdap.new(name: 'LDAP', host: 'ldap.example.com', port: 389, base_dn: 'dc=example,dc=com',
                            attr_login: 'uid', account: 'cn=admin', account_password: 'ldap-secret')
  ldap.save!

  # プロジェクト
  parent = Project.find_by(identifier: 'test') || Project.first
  unless parent
    # 空 DB(load_default_data のみ)の場合は親プロジェクトと既存チケットを作る
    parent = Project.create!(name: 'Test', identifier: 'test', is_public: true, enabled_module_names: Redmine::AccessControl.available_project_modules.map(&:to_s),
                             trackers: Tracker.all)
    Issue.create!(project: parent, tracker: Tracker.first, subject: 'Sample issue', author: admin, priority: IssuePriority.first)
  end
  base_issue = Issue.order(:id).first
  child = Project.new(name: '子プロジェクト', identifier: 'child-proj', is_public: false)
  child.parent = parent
  child.enabled_module_names = %w[issue_tracking time_tracking news documents files wiki repository boards]
  child.trackers = Tracker.all
  child.save!
  role = Role.givable.first
  Member.create!(principal: u1, project: child, roles: [role])
  Member.create!(principal: g, project: child, roles: [Role.givable.last])

  ver = Version.create!(project: child, name: 'v1.0', effective_date: Date.new(2026, 12, 24), sharing: 'descendants')
  cat = IssueCategory.create!(project: child, name: 'バックエンド')

  cf_list = IssueCustomField.create!(name: 'リスト', field_format: 'list', possible_values: %w[赤 緑 青], is_for_all: true,
                                     multiple: true, tracker_ids: Tracker.pluck(:id))
  cf_bool = IssueCustomField.create!(name: 'フラグ', field_format: 'bool', is_for_all: true, tracker_ids: Tracker.pluck(:id),
                                     edit_tag_style: 'check_box')
  cf_user = IssueCustomField.create!(name: '担当2', field_format: 'user', is_for_all: true, tracker_ids: Tracker.pluck(:id),
                                     user_role: [role.id.to_s, ''])
  ProjectCustomField.create!(name: '顧客', field_format: 'string', visible: true)

  tracker = Tracker.first
  i1 = Issue.create!(project: child, tracker: tracker, subject: '親チケット 🎉', author: admin, priority: IssuePriority.default || IssuePriority.first,
                     description: "説明\n複数行", start_date: Date.new(2026, 10, 1), due_date: Date.new(2026, 10, 31),
                     estimated_hours: 1.5, fixed_version: ver, category: cat, assigned_to: u1,
                     custom_field_values: { cf_list.id.to_s => %w[赤 青], cf_bool.id.to_s => '1', cf_user.id.to_s => u1.id.to_s })
  i2 = Issue.create!(project: child, tracker: tracker, subject: '子チケット', author: u1, priority: IssuePriority.first, parent_issue_id: i1.id,
                     is_private: true)
  i1.reload
  i1.init_journal(admin, '更新コメント')
  i1.subject = '親チケット(改)'
  i1.save!
  IssueRelation.create!(issue_from: i1, issue_to: base_issue, relation_type: 'relates')
  Watcher.create!(watchable: i1, user: u1)
  TimeEntry.create!(project: child, issue: i1, user: u1, author: admin, hours: 0.1 + 0.2, spent_on: Date.new(2026, 10, 2),
                    activity: TimeEntryActivity.first, comments: '作業')

  # 添付(実ファイル)
  a1 = Attachment.new(author: admin, file: UpFile.new("hello attachment\n", 'readme.txt', 'text/plain'), container: i1)
  a1.save!
  a2 = Attachment.new(author: admin, file: UpFile.new("\x89PNG\r\n\x1a\nbinary".b, '画像ファイル.png', 'image/png'), container: i1)
  a2.save!
  a3 = Attachment.new(author: admin, file: UpFile.new('will be removed', 'gone.txt', 'text/plain'), container: i2)
  a3.save!
  File.delete(a3.diskfile)
  # 重複排除(同内容 → 同じファイルを共有)
  a4 = Attachment.new(author: u1, file: UpFile.new("hello attachment\n", 'copy.txt', 'text/plain'), container: i2)
  a4.save!

  # Wiki(gzip 圧縮の旧版を含む)
  wiki = child.wiki || Wiki.create!(project: child, start_page: 'Wiki')
  page = WikiPage.new(wiki: wiki, title: 'Wiki')
  page.content = WikiContent.new(text: "h1. はじめに\n\n本文 v1", author: admin)
  page.save!
  c = page.content
  c.text = "h1. はじめに\n\n本文 v2 #{'長文' * 50}"
  c.comments = '第 2 版'
  c.save!
  Setting.wiki_compression = ''
  c.reload
  c.text = '本文 v3(非圧縮)'
  c.save!
  WikiRedirect.create!(wiki: wiki, title: 'OldName', redirects_to: 'Wiki', redirects_to_wiki_id: wiki.id)

  # フォーラム・ニュース・文書
  board = Board.create!(project: child, name: '雑談', description: 'なんでも')
  m1 = Message.create!(board: board, author: u1, subject: 'こんにちは', content: '本文')
  Message.create!(board: board, author: admin, subject: 'RE: こんにちは', content: '返信', parent: m1)
  news = News.create!(project: child, author: admin, title: 'お知らせ', summary: '概要', description: '詳細')
  Comment.create!(commented: news, author: u1, content: 'コメント')
  Document.create!(project: child, category: DocumentCategory.first, title: '仕様書', description: 'v1')
  Reaction.create!(reactable: i1.journals.first, user: u1) if defined?(Reaction)

  # リポジトリ(パスワードは暗号化される)
  repo = Repository::Subversion.new(project: child, url: 'svn://svn.example.com/repo', login: 'svnuser', password: 'svn-secret',
                                    identifier: 'main', is_default: true)
  repo.save!(validate: false)
  repo.merge_extra_info('extra_report_last_commit' => '1')
  repo.save!(validate: false)

  # クエリ(各種 YAML 列)
  q = IssueQuery.new(name: '自分のチケット', project: child, user: u1, visibility: Query::VISIBILITY_ROLES, role_ids: [role.id])
  q.add_filter('status_id', 'o', [''])
  q.add_filter("cf_#{cf_list.id}", '=', ['赤'])
  q.add_filter('assigned_to_id', '=', ['me'])
  q.column_names = [:tracker, :subject, :"cf_#{cf_list.id}", :spent_hours]
  q.sort_criteria = [['priority', 'desc'], ['id', 'asc']]
  q.totalable_names = [:estimated_hours]
  q.group_by = 'tracker'
  q.options[:draw_relations] = '0'
  q.save!
  TimeEntryQuery.create!(name: '工数', user: admin, visibility: Query::VISIBILITY_PUBLIC,
                         filters: { 'spent_on' => { operator: 'lm', values: [''] } })

  # OAuth アプリ
  if defined?(Doorkeeper::Application)
    Doorkeeper::Application.create!(name: 'クライアント', redirect_uri: 'https://example.com/cb', scopes: 'view_issues', confidential: true)
  end
end
puts 'populated'
