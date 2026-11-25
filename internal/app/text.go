package app

import "fmt"

// text is every string the window shows, in one language.
type text struct {
	Whole, CommitsMode                                      string // mode buttons; CommitsMode takes the count
	Loading                                                 string
	Reload, Prev, Next                                      string // tooltips
	NoGuideMark                                             string // after a commit without a guide in the list
	NoGuide, GuideGoesAt                                    string
	WholeTitle, WorktreeTitle                               string
	Related                                                 string
	NoChanges                                               string
	OpenRight                                               string
	Added, Deleted, Unchanged                               string // badges
	Viewed                                                  string
	ToggleChanges, Close                                    string
	PickFile                                                string
	Problems                                                string // takes the count
	FileNotFound, Binary                                    string
	NoLinesInRange                                          string
	NotInComparison                                         string
	CannotSaveViewed                                        string
	CmpBranch, CmpUncommitted                               string // CmpBranch takes base and hash
	CmpCommit, CmpDirect                                    string // CmpCommit takes the hash; CmpDirect old and new
	WorkingTree                                             string
	DropHere, OpenFolder, OrRun                             string // welcome screen
	ChooseRepo, Review                                      string // folder dialog
	Recent, Forget                                          string // recent repositories on the welcome screen
	Questions, NewQuestion, AskSection, AskPlaceholder      string // questions to the agent
	Send, Stop, Delete, ReadOnly, WholeChange               string
	SectionLabel, Thinking, NoQuestions                     string // SectionLabel and Thinking take a name
	InstallCLI, CLIHint, CLIInstalled, CLIShadowed          string // CLIInstalled takes the link; CLIShadowed the link and the other command
	MoveTitle, MoveDetail, MoveButton, NotNow, DontAskAgain string // moving the app into Applications
}

var texts = map[string]*text{
	"en": {
		Whole: "Whole branch", CommitsMode: "By commit %d",
		Loading: "Loading…",
		Reload:  "Reload (R)", Prev: "Previous commit ([)", Next: "Next commit (])",
		NoGuideMark: " (no guide)",
		NoGuide:     "There is no guide for this yet. Showing the diff only.", GuideGoesAt: "Write the guide at: ",
		WholeTitle: "Changes on this branch", WorktreeTitle: "Uncommitted changes",
		Related:   "Related files",
		NoChanges: "No changes.",
		OpenRight: "Open on the right",
		Added:     "new", Deleted: "deleted", Unchanged: "unchanged",
		Viewed:        "Viewed",
		ToggleChanges: "Show or hide Changes (C)", Close: "Close",
		PickFile:     "Open a file from its name on the left, or from Changes",
		Problems:     "⚠ %d problems in the guide",
		FileNotFound: "File not found", Binary: "Binary file",
		NoLinesInRange:   "No lines in this range",
		NotInComparison:  "This file is not in the comparison",
		CannotSaveViewed: "Cannot save the viewed mark: ",
		CmpBranch:        "merge base with %s (%s) ↔ working tree", CmpUncommitted: "uncommitted changes (HEAD ↔ working tree)",
		CmpCommit: "commit %s", CmpDirect: "%s ↔ %s",
		WorkingTree:    "working tree",
		DropHere:       "Drop a Git repository folder here",
		OpenFolder:     "Open Folder…",
		OrRun:          "or run self-review open in a repository",
		ChooseRepo:     "Choose a Git repository to review",
		Review:         "Review",
		Recent:         "Recent repositories",
		Forget:         "Remove from the list",
		Questions:      "Questions",
		NewQuestion:    "New question",
		AskSection:     "Ask about this section",
		AskPlaceholder: "Ask a question or leave a review comment (⌘↩ to send)",
		Send:           "Send",
		Stop:           "Stop",
		Delete:         "Delete",
		ReadOnly:       "read-only",
		WholeChange:    "The whole change",
		SectionLabel:   "Section: %s",
		Thinking:       "%s is thinking…",
		InstallCLI:     "Install Command-Line Tool",
		MoveTitle:      "Move self-review to the Applications folder?",
		MoveDetail:     "It runs from outside Applications now. Moved, it stays where macOS expects apps, and the command-line tool can link to it.",
		MoveButton:     "Move to Applications",
		NotNow:         "Not Now",
		DontAskAgain:   "Don't ask again",
		CLIHint:        "Agents run self-review from the shell to check and open guides.",
		CLIInstalled:   "Installed: %s",
		CLIShadowed:    "Linked %s, but %s comes first on your PATH and runs instead.",
		NoQuestions:    "Ask the agent about the whole change (New question), a section of the guide, or lines: use the + next to a line number.",
	},
	"ja": {
		Whole: "ブランチ全体", CommitsMode: "コミット単位 %d",
		Loading: "読み込み中…",
		Reload:  "再読み込み (R)", Prev: "前のコミット ([)", Next: "次のコミット (])",
		NoGuideMark: "（ガイドなし）",
		NoGuide:     "この対象のガイドはまだありません。差分だけを表示しています。", GuideGoesAt: "ガイドを置く場所: ",
		WholeTitle: "ブランチ全体の変更", WorktreeTitle: "未コミットの変更",
		Related:   "関連ファイル",
		NoChanges: "変更はありません。",
		OpenRight: "右側で開く",
		Added:     "新規", Deleted: "削除", Unchanged: "変更なし",
		Viewed:        "既読",
		ToggleChanges: "Changes を開閉 (C)", Close: "閉じる",
		PickFile:     "左のファイル名か Changes からファイルを開きます",
		Problems:     "⚠ ガイドの形式に問題が %d 件",
		FileNotFound: "ファイルが見つかりません", Binary: "バイナリファイルです",
		NoLinesInRange:   "指定範囲に行がありません",
		NotInComparison:  "このファイルは比較範囲に存在しません",
		CannotSaveViewed: "既読を保存できません: ",
		CmpBranch:        "%s の分岐点 (%s) ↔ 作業ツリー", CmpUncommitted: "未コミットの変更（HEAD ↔ 作業ツリー）",
		CmpCommit: "コミット %s", CmpDirect: "%s ↔ %s",
		WorkingTree:    "作業ツリー",
		DropHere:       "Git リポジトリのフォルダをここにドロップ",
		OpenFolder:     "フォルダを開く…",
		OrRun:          "リポジトリで self-review open を実行しても開けます",
		ChooseRepo:     "レビューする Git リポジトリを選んでください",
		Review:         "レビュー",
		Recent:         "最近開いたリポジトリ",
		Forget:         "一覧から外す",
		Questions:      "質問",
		NewQuestion:    "新しい質問",
		AskSection:     "このセクションについて質問",
		AskPlaceholder: "質問やレビューコメント（⌘↩ で送信）",
		Send:           "送信",
		Stop:           "停止",
		Delete:         "削除",
		ReadOnly:       "読み取り専用",
		WholeChange:    "変更全体",
		SectionLabel:   "セクション: %s",
		Thinking:       "%s が考えています…",
		InstallCLI:     "コマンドラインツールをインストール",
		MoveTitle:      "self-review をアプリケーションフォルダに移動しますか？",
		MoveDetail:     "いまはアプリケーションフォルダの外で動いています。移動しておくと、コマンドラインツールのリンク先も変わらずに済みます。",
		MoveButton:     "アプリケーションフォルダに移動",
		NotNow:         "今はしない",
		DontAskAgain:   "今後は確認しない",
		CLIHint:        "エージェントはシェルから self-review を実行して、ガイドの検査や表示をします。",
		CLIInstalled:   "インストールしました: %s",
		CLIShadowed:    "%s にリンクしましたが、PATH で先に見つかる %s が使われます。",
		NoQuestions:    "変更全体（新しい質問）、ガイドのセクション、行（行番号の横の ＋）について、エージェントに質問できます。",
	},
}

// textFor returns the strings of lang, English when it has none.
func textFor(lang string) *text {
	if t, ok := texts[lang]; ok {
		return t
	}
	return texts["en"]
}

// comparison describes what a view compares.
func (t *text) comparison(mode, base, old, new string, short func(string) string) string {
	newSide := t.WorkingTree
	if new != "" {
		newSide = short(new)
	}
	switch mode {
	case "branch":
		return fmt.Sprintf(t.CmpBranch, base, short(old))
	case "uncommitted":
		return t.CmpUncommitted
	case "commit":
		return fmt.Sprintf(t.CmpCommit, short(new))
	}
	return fmt.Sprintf(t.CmpDirect, short(old), newSide)
}
