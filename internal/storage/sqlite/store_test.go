package sqlite

import "testing"

func TestSQLiteDSN(t *testing.T) {
	const query = "?_pragma=foreign_keys%28ON%29&_pragma=busy_timeout%285000%29"
	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "windows", path: "C:/Users/test/harflex.db", want: "file:///C:/Users/test/harflex.db" + query},
		{name: "windows lowercase", path: "c:/Users/test/harflex.db", want: "file:///c:/Users/test/harflex.db" + query},
		{name: "unix", path: "/Users/test/harflex.db", want: "file:///Users/test/harflex.db" + query},
		{name: "unix reserved characters", path: "/Users/test/harflex?#%.db", want: "file:///Users/test/harflex%3F%23%25.db" + query},
		{name: "windows reserved characters", path: "C:/Users/test/harflex#%.db", want: "file:///C:/Users/test/harflex%23%25.db" + query},
		{name: "memory", path: ":memory:", want: ":memory:" + query},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sqliteDSN(test.path); got != test.want {
				t.Fatalf("sqliteDSN(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}
