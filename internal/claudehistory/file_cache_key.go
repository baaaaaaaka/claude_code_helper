package claudehistory

import "os"

type fileCacheKey struct {
	Size               int64  `json:"size"`
	ModTimeUnixNano    int64  `json:"mtimeUnixNano"`
	Mode               uint32 `json:"mode"`
	Device             uint64 `json:"device,omitempty"`
	Inode              uint64 `json:"inode,omitempty"`
	ChangeTimeUnixNano int64  `json:"ctimeUnixNano,omitempty"`
	StrongIdentity     bool   `json:"strongIdentity"`
}

func currentFileCacheKey(path string) (fileCacheKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileCacheKey{}, err
	}
	key := fileCacheKey{
		Size:            info.Size(),
		ModTimeUnixNano: info.ModTime().UnixNano(),
		Mode:            uint32(info.Mode()),
	}
	populateFileCacheKeyPlatform(path, info, &key)
	return key, nil
}

func verifyStableFileCacheKey(path string, before fileCacheKey) (fileCacheKey, bool) {
	after, err := currentFileCacheKey(path)
	if err != nil {
		return fileCacheKey{}, false
	}
	return after, after == before
}

func (k fileCacheKey) usableForPersistentCache() bool {
	return k.StrongIdentity
}
