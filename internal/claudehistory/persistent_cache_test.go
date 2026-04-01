package claudehistory

import "testing"

func useTempClaudeHistoryCacheDir(t *testing.T) string {
	t.Helper()

	cacheDir := t.TempDir()
	prev := claudeHistoryUserCacheDirFn
	claudeHistoryUserCacheDirFn = func() (string, error) { return cacheDir, nil }
	resetSessionFileCache()
	resetSessionPersistentCacheForTest()
	resetHistoryIndexPersistentCacheForTest()
	resetProjectPersistentCacheForTest()

	t.Cleanup(func() {
		claudeHistoryUserCacheDirFn = prev
		resetSessionFileCache()
		resetSessionPersistentCacheForTest()
		resetHistoryIndexPersistentCacheForTest()
		resetProjectPersistentCacheForTest()
	})

	return cacheDir
}
