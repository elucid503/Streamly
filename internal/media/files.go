package media

import "errors"

// FileName finds the Febbox file name for a fid by walking the share tree.
func (r *Resolver) FileName(shareKey string, fid int) string {

	name, _ := fileNameForFID(r, shareKey, 0, fid)

	return name

}

func fileNameForFID(r *Resolver, shareKey string, parentID int, wantedFID int) (string, error) {

	entries, err := r.ListChildren(shareKey, parentID)

	if err != nil {
		return "", err
	}

	for _, entry := range entries {

		if entry.FID == wantedFID {
			return entry.FileName, nil
		}

		if entry.IsDir == 0 {
			continue
		}

		if name, err := fileNameForFID(r, shareKey, entry.FID, wantedFID); err == nil && name != "" {
			return name, nil
		}

	}

	return "", errors.New("file not found")

}
