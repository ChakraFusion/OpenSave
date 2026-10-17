package cloud

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/store"
)

// webDAV keeps snapshots in a folder on a WebDAV server (Nextcloud, a NAS).
type webDAV struct {
	s   *Service
	cfg store.CloudConfig
}

func (webDAV) listedFileCompletesRetry() bool { return false }

func (p webDAV) upload(f *os.File, size int64, fileName string) error {
	if p.cfg.URL == "" {
		return fmt.Errorf("no destination URL configured")
	}
	finalURL := joinURL(p.cfg.URL, url.PathEscape(fileName))

	// Read an existing public object before creating the staging object. This
	// preserves it even on a server that disregards Overwrite: F for an ordinary
	// same-name retry. A concurrent writer still relies on the WebDAV server
	// honouring the non-overwrite MOVE precondition below.
	finalPresent, finalHash, finalSize, err := p.remoteDigest(finalURL, size)
	if err != nil {
		return fmt.Errorf("WebDAV destination preflight: %w", err)
	}
	if finalPresent && finalSize != size {
		return fmt.Errorf("WebDAV immutable archive collision: %s already exists with different contents", fileName)
	}
	if finalPresent {
		expected, err := hashFileBounded(f, size)
		if err != nil {
			return fmt.Errorf("hash WebDAV upload source: %w", err)
		}
		if finalHash == expected {
			return nil
		}
		return fmt.Errorf("WebDAV immutable archive collision: %s already exists with different contents", fileName)
	}

	tempURL, err := p.temporaryURL()
	if err != nil {
		return fmt.Errorf("WebDAV temporary name: %w", err)
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			p.removeTemporary(tempURL)
		}
	}()

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek WebDAV upload source: %w", err)
	}
	h := sha256.New()
	counter := &byteCounter{}
	body := io.TeeReader(f, io.MultiWriter(h, counter))
	put, err := p.request(http.MethodPut, tempURL, body)
	if err != nil {
		return err
	}
	put.ContentLength = size
	put.Header.Set("Content-Type", "application/zip")
	put.Header.Set("If-None-Match", "*")
	resp, err := p.s.doTransfer(put)
	if err != nil {
		return fmt.Errorf("WebDAV staged upload: %w", err)
	}
	putErr := transferOK(resp)
	resp.Body.Close()
	if putErr != nil {
		return fmt.Errorf("WebDAV staged upload: %w", putErr)
	}
	if counter.n != size {
		return fmt.Errorf("WebDAV source changed during upload: read %d of %d bytes", counter.n, size)
	}
	var expected [sha256.Size]byte
	copy(expected[:], h.Sum(nil))

	present, stagedHash, stagedSize, err := p.remoteDigest(tempURL, size)
	if err != nil {
		return fmt.Errorf("WebDAV staged content verification: %w", err)
	}
	if !present || stagedSize != size || stagedHash != expected {
		return fmt.Errorf("WebDAV staged content verification failed: stored bytes differ for %s", fileName)
	}

	move, err := p.request("MOVE", tempURL, nil)
	if err != nil {
		return err
	}
	move.Header.Set("Destination", finalURL)
	move.Header.Set("Overwrite", "F")
	moveResp, err := p.s.doTransfer(move)
	if err != nil {
		if reconcileErr := p.reconcileMove(finalURL, expected, size, fileName); reconcileErr == nil {
			return nil
		} else {
			return fmt.Errorf("WebDAV MOVE outcome uncertain: %v; reconciliation failed: %w", err, reconcileErr)
		}
	}
	status := moveResp.StatusCode
	moveResp.Body.Close()

	if status == http.StatusPreconditionFailed {
		return p.reconcileMove(finalURL, expected, size, fileName)
	}
	// Some servers return HTTP 204 after a successful MOVE to a new name.
	// Both success responses follow verified staging without an extra download.
	// Neither response proves that a noncompliant server enforced Overwrite: F
	// atomically: a second GET cannot prove another writer was not overwritten.
	if status != http.StatusCreated && status != http.StatusNoContent {
		return fmt.Errorf("WebDAV safe publication requires MOVE with Overwrite: F (HTTP %d)", status)
	}
	cleanupTemp = false
	return nil
}

type byteCounter struct{ n int64 }

func (c *byteCounter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

func hashFileBounded(f *os.File, expectedSize int64) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return digest, err
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, expectedSize+1))
	if err != nil {
		return digest, err
	}
	if n != expectedSize {
		return digest, fmt.Errorf("source size changed: read %d of %d bytes", n, expectedSize)
	}
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func (p webDAV) request(method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, err
	}
	applyCustomHeaders(req, p.cfg.HeadersJSON)
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)
	return req, nil
}

func (p webDAV) temporaryURL() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	name := ".opensave-" + hex.EncodeToString(nonce) + ".part"
	return joinURL(p.cfg.URL, url.PathEscape(name)), nil
}

// remoteDigest proves the bytes the server returns, not just their reported
// length. The size+1 limit keeps a broken endpoint from turning verification
// into an unbounded download.
func (p webDAV) remoteDigest(target string, expectedSize int64) (bool, [sha256.Size]byte, int64, error) {
	var digest [sha256.Size]byte
	req, err := p.request(http.MethodGet, target, nil)
	if err != nil {
		return false, digest, 0, err
	}
	// Compare the stored bytes themselves. Automatic content coding would hash
	// a decoded response instead of the representation that was PUT.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := p.s.doTransfer(req)
	if err != nil {
		return false, digest, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, digest, 0, nil
	}
	if err := transferOK(resp); err != nil {
		return false, digest, 0, err
	}
	if resp.ContentLength >= 0 && resp.ContentLength != expectedSize {
		return true, digest, resp.ContentLength, nil
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, expectedSize+1))
	if err != nil {
		return false, digest, n, fmt.Errorf("read back WebDAV object: %w", err)
	}
	copy(digest[:], h.Sum(nil))
	return true, digest, n, nil
}

func (p webDAV) reconcileMove(finalURL string, expected [sha256.Size]byte, size int64, fileName string) error {
	present, digest, count, err := p.remoteDigest(finalURL, size)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("WebDAV destination is absent after MOVE")
	}
	if count == size && digest == expected {
		return nil
	}
	return fmt.Errorf("WebDAV immutable archive collision: %s exists with different contents", fileName)
}

func (p webDAV) removeTemporary(target string) {
	req, err := p.request(http.MethodDelete, target, nil)
	if err != nil {
		p.warnTemporaryCleanup("request creation failed")
		return
	}
	resp, err := p.s.doTransfer(req)
	if err != nil {
		p.warnTemporaryCleanup("request failed")
		return
	}
	defer resp.Body.Close()
	// 404 is expected when MOVE has already consumed this staging object.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
		return
	default:
		p.warnTemporaryCleanup(fmt.Sprintf("returned HTTP %d", resp.StatusCode))
	}
}

func (p webDAV) warnTemporaryCleanup(reason string) {
	if p.s != nil && p.s.Log != nil {
		// Never log the target URL: it could contain sensitive credentials.
		p.s.Log("warn", "cloud: WebDAV staging cleanup "+reason+"; a temporary .part file may remain")
	}
}

// list issues a Depth-1 PROPFIND and parses the multistatus XML.
func (p webDAV) list() ([]CloudFile, error) {
	if p.cfg.URL == "" {
		return nil, fmt.Errorf("no destination URL configured")
	}
	req, err := http.NewRequest("PROPFIND", p.cfg.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "text/xml")
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)

	resp, err := p.s.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("WebDAV list returned HTTP %d", resp.StatusCode)
	}

	var ms struct {
		Responses []struct {
			Href  string `xml:"href"`
			Props []struct {
				Length   string `xml:"prop>getcontentlength"`
				Modified string `xml:"prop>getlastmodified"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := xml.Unmarshal(raw, &ms); err != nil {
		return nil, fmt.Errorf("parse WebDAV multistatus: %w", err)
	}

	baseName := path.Base(strings.TrimSuffix(p.cfg.URL, "/"))
	var files []CloudFile
	for _, r := range ms.Responses {
		href, err := url.PathUnescape(strings.TrimSpace(r.Href))
		if err != nil {
			href = r.Href
		}
		name := path.Base(strings.TrimSuffix(href, "/"))
		if name == "" || name == baseName {
			continue
		}
		if strings.HasPrefix(name, ".opensave-") && strings.HasSuffix(name, partialSuffix) {
			continue
		}
		f := CloudFile{Name: name, CreatedTime: time.Now().UTC().Format(time.RFC3339)}
		for _, prop := range r.Props {
			if prop.Length != "" {
				f.SizeBytes, _ = strconv.ParseInt(strings.TrimSpace(prop.Length), 10, 64)
			}
			if prop.Modified != "" {
				if t, err := time.Parse(time.RFC1123, strings.TrimSpace(prop.Modified)); err == nil {
					f.CreatedTime = t.UTC().Format(time.RFC3339)
				}
			}
		}
		files = append(files, f)
	}
	return files, nil
}

func (p webDAV) download(fileName, localPath string) error {
	req, err := http.NewRequest(http.MethodGet, joinURL(p.cfg.URL, url.PathEscape(fileName)), nil)
	if err != nil {
		return err
	}
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)
	if err := p.s.fetchToFile(req, localPath); err != nil {
		return fmt.Errorf("WebDAV: %w", err)
	}
	return nil
}

func (p webDAV) remove(f CloudFile) error {
	req, err := http.NewRequest(http.MethodDelete, joinURL(p.cfg.URL, url.PathEscape(f.Name)), nil)
	if err != nil {
		return err
	}
	applyCustomHeaders(req, p.cfg.HeadersJSON)
	applyBasicAuth(req, p.cfg.Username, p.cfg.Password)
	return p.s.doOK(req)
}
