package cloud

import (
	"os"

	"github.com/opensave/opensave/internal/store"
)

// provider is one kind of place snapshots are kept. Each lives in its own
// file (provider_*.go); this is all the Service knows of them. They used to
// be six cases of one switch in each of Upload, List, Download and Delete —
// four switches that had to agree, in a file where a change to Dropbox meant
// reading past Google Drive, OneDrive and WebDAV to find it.
type provider interface {
	// upload stores the open file under fileName. Providers that give archive
	// names immutable publication semantics may accept an identical retry but
	// must not replace different bytes already stored under the same name.
	// size is f's length.
	upload(f *os.File, size int64, fileName string) error
	// list returns every snapshot zip stored, not one page of them.
	list() ([]CloudFile, error)
	// download writes the stored fileName to localPath.
	download(fileName, localPath string) error
	// remove deletes one stored file.
	remove(f CloudFile) error
}

// retryListingPolicy is implemented by a provider when seeing a failed
// upload's name in a later listing is not enough to prove that upload complete.
// Most provider APIs make the upload request itself the commit. WebDAV can
// lose a MOVE response after the server committed it, so it has to compare the
// listed object with the source by entering upload again.
type retryListingPolicy interface {
	listedFileCompletesRetry() bool
}

// providerFor returns the provider cfg names, or false for one this build
// does not know.
func (s *Service) providerFor(cfg store.CloudConfig) (provider, bool) {
	switch cfg.Provider {
	case "local":
		return localFolder{cfg: cfg}, true
	case "webdav":
		return webDAV{s: s, cfg: cfg}, true
	case "webhook":
		return webhook{s: s, cfg: cfg}, true
	case "google_drive":
		return googleDrive{s: s, cfg: cfg}, true
	case "dropbox":
		return dropbox{s: s}, true
	case "onedrive":
		return oneDrive{s: s}, true
	}
	return nil, false
}
