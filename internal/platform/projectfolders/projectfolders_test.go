//go:build unix

package projectfolders

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// tree is a temporary folder with its symbolic links resolved (on macOS the temporary folders sit behind one).
func tree(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func detected(t *testing.T, cwd string) string {
	t.Helper()
	p, err := Detect(cwd)
	if err != nil {
		t.Fatal(err)
	}
	return p.Root()
}

func TestTheProjectOfAFolderInAGitRepositoryIsTheTopOfTheRepository(t *testing.T) {
	// Given a repository with a sub-folder
	root := mkdir(t, tree(t), "app")
	mkdir(t, root, ".git")
	sub := mkdir(t, root, "web", "src")

	for name, cwd := range map[string]string{"its top": root, "a sub-folder": sub} {
		// When the project of that folder is detected
		got := detected(t, cwd)

		// Then it is the top of the repository
		if got != root {
			t.Errorf("from %s: project = %q, want %q", name, got, root)
		}
	}
}

func TestTheProjectOfALinkedWorktreeIsItsOwnFolder(t *testing.T) {
	// Given a worktree, whose .git is a file naming the repository it belongs to
	dir := tree(t)
	worktree := mkdir(t, dir, "worktrees", "feature")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: ../../repo/.git/worktrees/feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// When the project of one of its sub-folders is detected
	got := detected(t, mkdir(t, worktree, "pkg"))

	// Then it is the worktree, not the repository it was made from
	if got != worktree {
		t.Errorf("project = %q, want %q", got, worktree)
	}
}

func TestTheProjectOfAFolderOutsideAnyRepositoryIsTheFolderItself(t *testing.T) {
	// Given a folder in no repository
	dir := mkdir(t, tree(t), "notes", "2026")

	// When its project is detected
	// Then it is that folder
	if got := detected(t, dir); got != dir {
		t.Errorf("project = %q, want %q", got, dir)
	}
}

func TestTheProjectOfAFolderReachedThroughALinkIsTheOneTheLinkLeadsTo(t *testing.T) {
	// Given a repository and a link to one of its sub-folders
	dir := tree(t)
	root := mkdir(t, dir, "app")
	mkdir(t, root, ".git")
	sub := mkdir(t, root, "web")
	link := filepath.Join(dir, "shortcut")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}

	// When the project of the link is detected
	// Then it is the repository, found from where the link leads
	if got := detected(t, link); got != root {
		t.Errorf("project = %q, want %q", got, root)
	}
}

func TestAProjectGivenByItsFolderIsResolvedButNotSearchedForGit(t *testing.T) {
	// Given a link to a folder inside a repository
	dir := tree(t)
	root := mkdir(t, dir, "app")
	mkdir(t, root, ".git")
	sub := mkdir(t, root, "web")
	link := filepath.Join(dir, "shortcut")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}

	// When it is given as the project
	p, err := Of(link)

	// Then the project is that folder, as asked
	if err != nil || p.Root() != sub {
		t.Errorf("project = %q, %v, want %q", p.Root(), err, sub)
	}
}

func TestAFolderIsResolvedUnderTheLinksOfTheOnesThatExist(t *testing.T) {
	dir := tree(t)
	real := mkdir(t, dir, "real")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ in, want string }{
		"a link":                                {link, real},
		"a folder under a link":                 {link + "/sub", real + "/sub"},
		"a folder since deleted, under a link":  {link + "/gone/deeper", real + "/gone/deeper"},
		"a folder that never existed":           {dir + "/nowhere/at/all", dir + "/nowhere/at/all"},
		"an untidy path":                        {link + "/./gone/../sub", real + "/sub"},
		"a link loop, taken as written":         {loop + "/x", loop + "/x"},
		"a relative path, which names no place": {"app/web", ""},
		"an empty path":                         {"", ""},
	} {
		if got := Resolve(c.in); got != c.want {
			t.Errorf("%s: Resolve(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestADotDotAfterALinkGoesUpFromWhereTheLinkLeads(t *testing.T) {
	// Given a link in the app to a folder of another project, and a folder next to that target that was deleted
	dir := tree(t)
	app := mkdir(t, dir, "a")
	target := mkdir(t, dir, "b", "sub")
	link := filepath.Join(app, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// When a path that goes through the link, up one folder and down into the deleted one is resolved
	got := Resolve(link + "/../gone")

	// Then the ".." is taken from the target of the link, so the path lies in the other project, not in the app
	if want := filepath.Join(dir, "b", "gone"); got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// ignoresCase skips the test on a volume that tells "app" from "APP": there, a folder written in another case is another folder.
func ignoresCase(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, strings.ToUpper(name))); err != nil {
		t.Skip("this volume is case-sensitive")
	}
}

func TestAFolderWrittenInAnotherCaseIsTheOneOnDiskOnAVolumeThatIgnoresCase(t *testing.T) {
	// Given a project folder and a sub-folder created as MyApp and Web, on a volume that ignores case
	dir := tree(t)
	app := mkdir(t, dir, "MyApp")
	sub := mkdir(t, app, "Web")
	ignoresCase(t, dir, "MyApp")

	// When a path to the sub-folder is resolved in lower case, through a link written in lower case too
	link := filepath.Join(dir, "shortcut")
	if err := os.Symlink(strings.ToLower(sub), link); err != nil {
		t.Fatal(err)
	}

	// Then it is the name the folder has on disk, whichever the case it was written in
	for name, in := range map[string]string{"lower case": strings.ToLower(sub), "upper case": filepath.Join(dir, "MYAPP", "WEB"), "a link": link} {
		if got := Resolve(in); got != sub {
			t.Errorf("%s: Resolve(%q) = %q, want %q", name, in, got, sub)
		}
	}
}

func TestAProjectGivenInAnotherCaseKeepsTheFoldersTheLogsWroteInTheirOwn(t *testing.T) {
	// Given sessions that logged their folder in the case it has on disk and in another one, on a volume that ignores case
	dir := tree(t)
	app := mkdir(t, dir, "MyApp")
	mkdir(t, dir, "Other")
	ignoresCase(t, dir, "MyApp")
	db := indexOf(t, app, strings.ToLower(app)+"/web", dir+"/Other")

	// When the project is given in yet another case
	p, err := Of(filepath.Join(dir, "MYAPP"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Folders(db, p)

	// Then it is the project on disk, and both spellings of its folder are in it
	if want := fmt.Sprint([]string{app, strings.ToLower(app) + "/web"}); err != nil || fmt.Sprint(got) != want || p.Root() != app {
		t.Errorf("project %q, folders = %v, %v, want %s", p.Root(), got, err, want)
	}
}

// indexOf is a database whose usage_folders holds folders, as the index's published view does.
func indexOf(t *testing.T, folders ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE usage_folders (folder TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		if _, err := db.Exec(`INSERT INTO usage_folders VALUES (?)`, f); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestOnlyTheFoldersOfTheProjectAreKept(t *testing.T) {
	// Given sessions that worked in the project, below it, in a sibling sharing its name as a prefix, in another
	// project, reached through a link, and in folders that name no place
	dir := tree(t)
	app := mkdir(t, dir, "app")
	sub := mkdir(t, app, "web")
	sibling := mkdir(t, dir, "app-2")
	other := mkdir(t, dir, "other")
	link := filepath.Join(dir, "shortcut")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	db := indexOf(t, app, sub, sibling, other, link, app+"/../app-2", "web/src", "", app+"/deleted")
	p, err := Of(app)
	if err != nil {
		t.Fatal(err)
	}

	// When the folders of the project are asked for
	got, err := Folders(db, p)

	// Then those in the project are kept, the link among them, and the others are left out
	want := fmt.Sprint([]string{app, sub, link, app + "/deleted"})
	if err != nil || fmt.Sprint(got) != want {
		t.Errorf("folders = %v, %v, want %s", got, err, want)
	}
}

func TestAProjectFolderNamedLikeSQLIsJustAFolder(t *testing.T) {
	// Given an index whose folders hold SQL metacharacters, and a project that holds one of them
	dir := tree(t)
	hostile := mkdir(t, dir, `app'"; DROP TABLE usage_folders; --`)
	db := indexOf(t, hostile, dir+"/elsewhere")
	if _, err := db.Exec(`CREATE TABLE usage (folder TEXT, n INTEGER); INSERT INTO usage VALUES (?, 1), (?, 2), (NULL, 3)`, hostile, dir+"/elsewhere"); err != nil {
		t.Fatal(err)
	}
	p, err := Of(hostile)
	if err != nil {
		t.Fatal(err)
	}

	// When rows are narrowed to the project
	cond, args, err := Condition(db, p, "folder")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	err = db.QueryRow(`SELECT COALESCE(SUM(n), 0) FROM usage WHERE `+cond, args...).Scan(&n)

	// Then only the project's rows are kept and nothing was run but the question
	if err != nil || n != 1 {
		t.Errorf("sum = %d, %v", n, err)
	}
	if _, err := db.Exec(`SELECT 1 FROM usage_folders`); err != nil {
		t.Errorf("usage_folders: %v", err)
	}
}

func TestNarrowingToNoProjectKeepsEveryRow(t *testing.T) {
	cond, args, err := Condition(indexOf(t, "/work/app"), kernel.Project{}, "folder")
	if err != nil || cond != "" || args != nil {
		t.Errorf("condition = %q %v %v", cond, args, err)
	}
}

func TestAProjectWithNoSessionKeepsNoRow(t *testing.T) {
	// Given a project in which no session worked
	dir := tree(t)
	db := indexOf(t, dir+"/elsewhere")
	if _, err := db.Exec(`CREATE TABLE usage (folder TEXT); INSERT INTO usage VALUES (?)`, dir+"/elsewhere"); err != nil {
		t.Fatal(err)
	}
	p, _ := Of(mkdir(t, dir, "app"))

	// When rows are narrowed to it
	cond, args, err := Condition(db, p, "folder")
	var n int
	if err == nil {
		err = db.QueryRow(`SELECT COUNT(*) FROM usage WHERE `+cond, args...).Scan(&n)
	}

	// Then none is kept
	if err != nil || n != 0 {
		t.Errorf("rows kept = %d, %v", n, err)
	}
}
