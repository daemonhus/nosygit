package watchers

import "testing"

func TestClassifyFileOperation(t *testing.T) {
	tests := []struct {
		name           string
		hasFrom        bool
		hasTo          bool
		fromPath       string
		toPath         string
		expectedPath   string
		expectedOp     string
		expectedExists bool
	}{
		{
			name:           "create",
			hasFrom:        false,
			hasTo:          true,
			toPath:         "new/file.go",
			expectedPath:   "new/file.go",
			expectedOp:     "CREATE",
			expectedExists: true,
		},
		{
			name:           "remove",
			hasFrom:        true,
			hasTo:          false,
			fromPath:       "old/file.go",
			expectedPath:   "old/file.go",
			expectedOp:     "REMOVE",
			expectedExists: true,
		},
		{
			name:           "edit",
			hasFrom:        true,
			hasTo:          true,
			fromPath:       "file_old.go",
			toPath:         "file_new.go",
			expectedPath:   "file_new.go",
			expectedOp:     "EDIT",
			expectedExists: true,
		},
		{
			name:           "none",
			hasFrom:        false,
			hasTo:          false,
			expectedPath:   "",
			expectedOp:     "",
			expectedExists: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, op, ok := classifyFileOperation(tc.hasFrom, tc.hasTo, tc.fromPath, tc.toPath)
			if ok != tc.expectedExists {
				t.Fatalf("expected ok=%v got %v", tc.expectedExists, ok)
			}
			if path != tc.expectedPath {
				t.Fatalf("expected path=%s got %s", tc.expectedPath, path)
			}
			if op != tc.expectedOp {
				t.Fatalf("expected op=%s got %s", tc.expectedOp, op)
			}
		})
	}
}
