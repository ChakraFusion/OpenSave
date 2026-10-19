package daemon

import (
	"fmt"
	"time"

	"github.com/opensave/opensave/internal/cloud"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
)

// maxRetriesPerCheck bounds how many failed uploads one cloud check sends
// again, so a long outage's backlog goes up over a few checks rather than
// holding one for as long as all of it takes.
const maxRetriesPerCheck = 10

// noteUploadFailed keeps a snapshot whose cloud copy did not go up, to be sent
// again by retryFailedUploads.
func (d *Daemon) noteUploadFailed(zipPath, remoteName string) {
	gameID, _, _, ok := snapshot.ParseExportEntryName(remoteName)
	if !ok {
		return
	}
	if err := d.Store.NoteCloudUploadFailed(gameID, remoteName, zipPath, time.Now().UnixMilli()); err != nil {
		d.Log.Log("warn", err.Error())
	}
}

// noteUploadDue keeps a snapshot's cloud copy as owed before it is sent, the
// way a failed one is kept: forgotten once it is up (runCloudUpload), sent
// again by retryFailedUploads otherwise. Noted only when the cloud is set up
// to take it and can be listed, which is what retrying needs; otherwise every
// snapshot would be kept for a retry that can never run.
func (d *Daemon) noteUploadDue(zipPath, remoteName string) {
	cfg, err := d.Store.GetCloudConfig()
	if err != nil || !cloud.Ready(cfg) || !cloudListable(cfg.Provider) {
		return
	}
	d.noteUploadFailed(zipPath, remoteName)
}

// startSending marks a cloud copy as being sent, and reports false when it
// already is.
func (d *Daemon) startSending(remoteName string) bool {
	d.sendingMu.Lock()
	defer d.sendingMu.Unlock()
	if d.sending[remoteName] {
		return false
	}
	if d.sending == nil {
		d.sending = map[string]bool{}
	}
	d.sending[remoteName] = true
	return true
}

func (d *Daemon) isSending(remoteName string) bool {
	d.sendingMu.Lock()
	defer d.sendingMu.Unlock()
	return d.sending[remoteName]
}

func (d *Daemon) doneSending(remoteName string) {
	d.sendingMu.Lock()
	delete(d.sending, remoteName)
	d.sendingMu.Unlock()
}

// retryFailedUploads sends again the cloud copies that failed to go up. It
// runs from the cloud check once a listing has worked, which is the proof
// that the provider can be reached again. Each is sent once per check at
// most, and forgotten once it is there — sent by this, by hand, or by another
// path — or once the snapshot is gone here. A WebDAV name alone is not proof:
// its upload path compares the bytes before the retry is forgotten.
func (d *Daemon) retryFailedUploads(inCloud []cloud.CloudFile) {
	pending, err := d.Store.CloudRetries()
	if err != nil || len(pending) == 0 {
		return
	}
	present := make(map[string]bool, len(inCloud))
	for _, f := range inCloud {
		present[f.Name] = true
	}
	listingCompletesRetry := d.Cloud.ListedFileCompletesRetry()
	var send []store.CloudRetry
	for _, p := range pending {
		if (present[p.RemoteName] && listingCompletesRetry) || !snapshot.ArchiveExists(p.ZipPath) {
			_ = d.Store.ForgetCloudRetry(p.RemoteName)
			continue
		}
		if d.isSending(p.RemoteName) {
			continue // owed, and on its way up now
		}
		send = append(send, p)
	}
	if len(send) == 0 {
		return
	}
	if len(send) > maxRetriesPerCheck {
		send = send[:maxRetriesPerCheck]
	}
	what := "1 snapshot"
	if len(send) > 1 {
		what = fmt.Sprintf("%d snapshots", len(send))
	}
	d.Log.Log("info", "cloud: sending again "+what+" that did not go up earlier")
	for _, p := range send {
		d.uploads.Add()
		d.runCloudUpload(p.ZipPath, p.RemoteName, d.Log)
	}
}
