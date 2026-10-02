package importer

// idset は取り込んだ行の ID 集合。
type idset map[int64]struct{}

func (s idset) add(id int64)         { s[id] = struct{}{} }
func (s idset) has(id int64) bool    { _, ok := s[id]; return ok }
func (s idset) hasRef(id int64) bool { return id > 0 && s.has(id) }
func newIDSet() idset                { return idset{} }

// state は変換中に参照する取り込み済みデータ。
type state struct {
	// settings はソースの設定値(名前 → 生の値。Setting#value 相当の文字列/YAML)。
	settings map[string]string

	authSources idset
	// principals はプリンシパル ID → kind。
	principals map[int64]string
	// users は user_accounts を持つプリンシパル(user / anonymous_user)。
	users       idset
	anonymousID int64
	// mailNotification はユーザー ID → users.mail_notification(正規化済み、"" は未設定)。
	mailNotification map[int64]string
	// prefNotify はユーザー ID → user_preferences の通知関連値。
	prefNotify map[int64]prefNotify
	// userOrder は user_accounts を作ったユーザー ID(挿入順)。
	userOrder []int64
	// groupUsers はグループ ID → 所属ユーザー ID。
	groupUsers map[int64][]int64

	statuses idset
	// statusOrder は position 順のステータス ID。
	statusOrder []int64
	trackers    idset
	// trackerDefault はトラッカー ID → 既定ステータス。
	trackerDefault     map[int64]int64
	trackerOrder       []int64
	priorities         idset
	priorityOrder      []int64
	defaultPriority    int64
	docCategories      idset
	defaultDocCategory int64
	// activities は活動 ID → project_id(システム活動は 0)。
	activities map[int64]int64
	// activityParent は破棄したプロジェクト上書き活動 ID → 親活動 ID(time_entries の付け替え用)。
	activityRemap   map[int64]int64
	defaultActivity int64
	// pendingActivities は enumerations から読んだ TimeEntryActivity(projects 取り込み後に処理)。
	pendingActivities []rec
	// roles はロール ID → builtin。
	roles map[int64]int64
	// roleDefaultActivity はロール ID → default_time_entry_activity_id(活動の取り込み後に設定)。
	roleDefaultActivity map[int64]int64

	// projects はプロジェクト ID → 親 ID(0 = ルート)。
	projects map[int64]int64
	// projectTrackers はプロジェクト ID → トラッカー ID。
	projectTrackers map[int64][]int64
	// projectDefaults はプロジェクトの default_version_id / default_issue_query_id(後で UPDATE)。
	projectDefaultVersion map[int64]int64
	projectDefaultQuery   map[int64]int64
	// modules は project_modules の ID。
	modules idset

	// customFields はカスタムフィールド ID → 情報。
	customFields   map[int64]*cfInfo
	cfEnumerations idset

	// members はメンバー ID → (principal, project)。
	members      map[int64][2]int64
	memberByPair map[[2]int64]int64
	// memberNotify は mail_notification が真のメンバー ID。
	memberNotify []int64
	memberRoles  idset

	versions   map[int64]int64
	categories map[int64]int64

	// issues はチケット ID → project_id。
	issues   map[int64]int64
	journals idset

	timeEntries idset
	documents   idset
	news        idset
	comments    idset
	boards      map[int64]int64
	messages    idset
	wikis       map[int64]int64
	// wikiByProject はプロジェクト ID → wiki ID。
	wikiPages map[int64]int64
	// wikiCurrent はページ ID → wiki_contents.version。
	wikiCurrent map[int64]int64

	repositories idset
	changesets   idset
	queries      map[int64]string

	customValues idset
	attachments  idset
	// attachmentPaths は取り込んだ添付が参照するファイル(files/ からの相対パス → 添付 ID)。
	attachmentPaths map[string]int64
}

// cfInfo はカスタムフィールドの情報。
type cfInfo struct {
	ownerKind string
	format    string
	multiple  bool
}

func (im *imp) initState() {
	im.st = state{
		settings:              map[string]string{},
		authSources:           newIDSet(),
		principals:            map[int64]string{},
		users:                 newIDSet(),
		mailNotification:      map[int64]string{},
		groupUsers:            map[int64][]int64{},
		statuses:              newIDSet(),
		trackers:              newIDSet(),
		trackerDefault:        map[int64]int64{},
		priorities:            newIDSet(),
		docCategories:         newIDSet(),
		activities:            map[int64]int64{},
		activityRemap:         map[int64]int64{},
		roles:                 map[int64]int64{},
		roleDefaultActivity:   map[int64]int64{},
		projects:              map[int64]int64{},
		projectTrackers:       map[int64][]int64{},
		projectDefaultVersion: map[int64]int64{},
		projectDefaultQuery:   map[int64]int64{},
		modules:               newIDSet(),
		customFields:          map[int64]*cfInfo{},
		cfEnumerations:        newIDSet(),
		members:               map[int64][2]int64{},
		memberByPair:          map[[2]int64]int64{},
		memberRoles:           newIDSet(),
		versions:              map[int64]int64{},
		categories:            map[int64]int64{},
		issues:                map[int64]int64{},
		journals:              newIDSet(),
		timeEntries:           newIDSet(),
		documents:             newIDSet(),
		news:                  newIDSet(),
		comments:              newIDSet(),
		boards:                map[int64]int64{},
		messages:              newIDSet(),
		wikis:                 map[int64]int64{},
		wikiPages:             map[int64]int64{},
		wikiCurrent:           map[int64]int64{},
		repositories:          newIDSet(),
		changesets:            newIDSet(),
		queries:               map[int64]string{},
		customValues:          newIDSet(),
		attachments:           newIDSet(),
		attachmentPaths:       map[string]int64{},
	}
}

// principal は参照先プリンシパルが存在するか。
func (im *imp) principal(id int64) bool {
	_, ok := im.st.principals[id]
	return id > 0 && ok
}

// authorOr は作成者 ID が存在すればそのまま、なければ匿名ユーザーを返し補完を記録する。
func (im *imp) authorOr(t *TableReport, rowID any, col string, id int64) int64 {
	if im.principal(id) {
		return id
	}
	if id == 0 {
		t.repair(rowID, "%s empty; set to anonymous user", col)
	} else {
		t.repair(rowID, "%s refers to missing user; set to anonymous user", col)
	}
	return im.st.anonymousID
}

// projectExists はプロジェクトが取り込まれているか。
func (im *imp) projectExists(id int64) bool {
	_, ok := im.st.projects[id]
	return id > 0 && ok
}
