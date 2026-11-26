// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dkorunic/nightglass/origin"
)

// GalleryImage is one image in the save folder (the app's PhotoGalleryFragment).
// Final is an Origin final image: it carries the run's Uuid, so cropping it
// gets the app's warning.
type GalleryImage struct {
	Record   *ImageRecord // what and where it was taken, when known
	Name     string
	Modified int64 // Unix seconds
	Favorite bool
	Final    bool
}

// galleryPath is name inside SaveDir, refusing anything but a plain image
// file name (names come from the frontend).
func (a *App) galleryPath(name string) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if name == "" || safeName(name, "") != name || (ext != ".png" && ext != ".jpg" && ext != ".jpeg") {
		return "", fmt.Errorf("not an image name: %q", name)
	}

	return filepath.Join(a.Settings().SaveDir, name), nil
}

// Gallery lists the save folder's images, newest first.
func (a *App) Gallery() ([]GalleryImage, error) {
	s := a.Settings()
	entries, err := os.ReadDir(s.SaveDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []GalleryImage
	for _, e := range entries {
		p, err := a.galleryPath(e.Name())
		if err != nil || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		g := GalleryImage{
			Name: e.Name(), Favorite: slices.Contains(s.Favorites, e.Name()),
			Final: a.finalOf(p, info), Modified: info.ModTime().Unix(),
		}
		if rec, ok := s.Images[e.Name()]; ok {
			g.Record = &rec
		}
		out = append(out, g)
	}
	// Newest first by when they were taken (mComparatorDate), else saved.
	when := func(g GalleryImage) int64 {
		if g.Record != nil && g.Record.TakenUnix != 0 {
			return g.Record.TakenUnix
		}

		return g.Modified
	}
	sort.SliceStable(out, func(i, j int) bool { return when(out[i]) > when(out[j]) })

	return out, nil
}

// isFinal reports whether a saved image carries a run's XMP DocumentID,
// which only final images get (snapshots have only their location).
func isFinal(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 64<<10) // the eXIf chunk follows IHDR
	n, _ := f.Read(head)

	return bytes.Contains(head[:n], []byte("xapMM:DocumentID"))
}

// finalSeen is isFinal's answer for a file of this size and time.
type finalSeen struct {
	mod   time.Time
	size  int64
	final bool
}

// finalOf is isFinal(p), remembered while the file keeps its size and
// modification time: each gallery listing read 64 KiB of every image.
func (a *App) finalOf(p string, fi fs.FileInfo) bool {
	a.mu.Lock()
	c, ok := a.finals[p]
	a.mu.Unlock()
	if ok && c.size == fi.Size() && c.mod.Equal(fi.ModTime()) {
		return c.final
	}
	c = finalSeen{mod: fi.ModTime(), size: fi.Size(), final: isFinal(p)}
	a.mu.Lock()
	a.finals[p] = c
	a.mu.Unlock()

	return c.final
}

// SetFavorite marks a saved image Favorite or Un-Favorite.
func (a *App) SetFavorite(name string, on bool) error {
	if _, err := a.galleryPath(name); err != nil {
		return err
	}
	a.update(func(s *Settings) {
		s.Favorites = slices.DeleteFunc(slices.Clone(s.Favorites), func(n string) bool { return n == name })
		if on {
			s.Favorites = append(s.Favorites, name)
		}
	})

	return nil
}

// SetImageTarget is the image info's Change Target…
// (ImageMetaDataFragment.onTargetSelected): the saved image is of target
// from now on. As the app's, only its record changes, not the file.
func (a *App) SetImageTarget(name, target string) error {
	p, err := a.galleryPath(name)
	if err != nil {
		return err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("no target chosen")
	}
	if _, err := os.Stat(p); err != nil {
		return err
	}
	a.update(func(s *Settings) {
		// A new map: copies of the settings handed out earlier share the old one.
		images := maps.Clone(s.Images)
		if images == nil {
			images = map[string]ImageRecord{}
		}
		rec := images[name]
		rec.Title = target
		images[name] = rec
		s.Images = images
	})

	return nil
}

// DeleteImages deletes saved images (the app asks first).
func (a *App) DeleteImages(names []string) error {
	var errs []error
	var gone []string
	for _, n := range names {
		p, err := a.galleryPath(n)
		if err == nil {
			err = os.Remove(p)
		}
		if err != nil {
			errs = append(errs, err)

			continue
		}
		gone = append(gone, n)
		a.mu.Lock()
		delete(a.finals, p)
		a.mu.Unlock()
	}
	if len(gone) > 0 { // one settings write, not two per image
		a.update(func(s *Settings) {
			s.Favorites = slices.DeleteFunc(slices.Clone(s.Favorites), func(n string) bool { return slices.Contains(gone, n) })
			// A new map: copies of the settings handed out earlier share the old one.
			images := maps.Clone(s.Images)
			for _, n := range gone {
				delete(images, n)
			}
			s.Images = images
		})
	}
	if len(errs) > 0 {
		if len(names) == 1 {
			return fmt.Errorf("alert:Unable to Delete Image: %w", errors.Join(errs...)) // the app's alerts
		}

		return fmt.Errorf("alert:Unable to delete Images: %w", errors.Join(errs...))
	}

	return nil
}

// RevealImage is the app's Share on a desktop: the file manager opens with
// the image selected (on Linux, its folder), to share from there.
func (a *App) RevealImage(name string) error {
	p, err := a.galleryPath(name)
	if err != nil {
		return err
	}

	return reveal(p)
}

// RevealSaved shows a file Nightglass saved (say the Origin's logs) in the
// file manager; name is a plain file name in SaveDir.
func (a *App) RevealSaved(name string) error {
	if name == "" || safeName(name, "") != name {
		return fmt.Errorf("not a saved file's name: %q", name)
	}

	return reveal(filepath.Join(a.Settings().SaveDir, name))
}

// reveal opens the platform's file manager on p (on Linux, its folder).
func reveal(p string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(context.Background(), "open", "-R", p)
	case "windows":
		cmd = exec.CommandContext(context.Background(), "explorer", "/select,", p)
	default:
		cmd = exec.CommandContext(context.Background(), "xdg-open", filepath.Dir(p)) //nolint:gosec // the folder of a file Nightglass saved
	}

	return cmd.Start()
}

// readImage decodes a saved image.
func (a *App) readImage(name string) ([]byte, image.Image, error) {
	p, err := a.galleryPath(name)
	if err != nil {
		return nil, nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))

	return b, img, err
}

// OverwriteImage replaces a saved image with its edited version, keeping
// its metadata, as the app's Overwrite: a PNG stays a PNG, a JPEG is saved
// at the app's quality 70.
func (a *App) OverwriteImage(name string, e Edit) error {
	orig, img, err := a.readImage(name)
	if err != nil {
		return fmt.Errorf("Failed to overwrite image: %w", err)
	}
	out := applyEdit(img, e)
	edited := editedJPEG
	if strings.EqualFold(filepath.Ext(name), ".png") {
		edited = editedPNG
	}
	b, err := edited(orig, out, e)
	if err != nil {
		return fmt.Errorf("Failed to overwrite image: %w", err)
	}
	p, _ := a.galleryPath(name)
	if err := os.WriteFile(p, b, 0o644); err != nil { //nolint:gosec // a saved image, readable like any other
		return fmt.Errorf("Failed to overwrite image: %w", err)
	}

	return nil
}

// editedPNG is an edited PNG keeping the original's EXIF, with the
// adjustments in its UserComment (BitmapWithMetadata).
func editedPNG(orig []byte, img image.Image, e Edit) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	b, err := origin.CopyExif(orig, buf.Bytes())
	if err != nil {
		return nil, err
	}

	return origin.SetUserComment(b, adjustmentsJSON(e))
}

// editedJPEG is the app's saveEditedImageToUri: the edited image as a
// quality-70 JPEG carrying the original's EXIF, with the adjustments in its
// UserComment.
func editedJPEG(orig []byte, img image.Image, e Edit) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		return nil, err
	}
	t, err := origin.WithUserComment(origin.ExifOf(orig), adjustmentsJSON(e))
	if err != nil {
		return nil, err
	}

	return origin.JPEGWithExif(buf.Bytes(), t)
}

// adjustmentsJSON is the app's ImageAdjustments.toJson.
func adjustmentsJSON(e Edit) string {
	type adj struct {
		Version    int     `json:"version"`
		Brightness float64 `json:"brightness"`
		Contrast   float64 `json:"contrast"`
		Saturation float64 `json:"saturation"`
		Red        float64 `json:"red"`
		Green      float64 `json:"green"`
		Blue       float64 `json:"blue"`
	}
	out, err := json.Marshal(map[string]adj{"SkySafariAdjustments": {1, e.Brightness, e.Contrast, e.Saturation, e.Red, e.Green, e.Blue}})
	if err != nil { // a NaN slider value
		return ""
	}

	return string(out)
}

// ExportImage saves an edited copy as a new JPEG beside the original, as
// the app's Export Copy saves "SkySafari_<ms>.jpg" to the camera roll; it
// returns the copy's name.
func (a *App) ExportImage(name string, e Edit) (string, error) {
	orig, img, err := a.readImage(name)
	if err != nil {
		return "", fmt.Errorf("Failed to save image: %w", err)
	}
	b, err := editedJPEG(orig, applyEdit(img, e), e)
	if err != nil {
		return "", fmt.Errorf("Failed to save image: %w", err)
	}
	copyName := fmt.Sprintf("Nightglass_%d.jpg", a.now().UnixMilli())
	p, _ := a.galleryPath(copyName)
	if err := os.WriteFile(p, b, 0o644); err != nil { //nolint:gosec // a saved image, readable like any other
		return "", fmt.Errorf("Failed to save image: %w", err)
	}
	a.update(func(s *Settings) { // the copy is of the same target
		if rec, ok := s.Images[name]; ok {
			s.Images[copyName] = rec
		}
	})

	return copyName, nil
}

// SaveShared saves the share screen's copy of name (a PNG data URL drawn by
// the frontend, with the chosen details on it) next to it as
// "<name>-shared.png", and returns the copy's name.
func (a *App) SaveShared(name, dataURL string) (string, error) {
	if _, err := a.galleryPath(name); err != nil {
		return "", err
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		return "", errors.New("Failed to save image: not a PNG")
	}
	b, err := base64.StdEncoding.DecodeString(dataURL[len(prefix):])
	if err != nil || !bytes.HasPrefix(b, []byte("\x89PNG")) {
		return "", errors.New("Failed to save image: not a PNG")
	}
	copyName := strings.TrimSuffix(name, filepath.Ext(name)) + "-shared.png"
	p, err := a.galleryPath(copyName)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, b, 0o644); err != nil { //nolint:gosec // a shared image, readable like any other
		return "", fmt.Errorf("Failed to save image: %w", err)
	}
	a.update(func(s *Settings) {
		if rec, ok := s.Images[name]; ok {
			s.Images[copyName] = rec
		}
	})

	return copyName, nil
}
