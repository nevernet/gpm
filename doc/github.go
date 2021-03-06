// Copyright (c) 2013 GPMGo Members. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package doc

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/GPMGo/gpm/utils"
)

var (
	githubRawHeader = http.Header{"Accept": {"application/vnd.github-blob.raw"}}
	GithubPattern   = regexp.MustCompile(`^github\.com/(?P<owner>[a-z0-9A-Z_.\-]+)/(?P<repo>[a-z0-9A-Z_.\-]+)(?P<dir>/[a-z0-9A-Z_.\-/]*)?$`)
	githubCred      string
)

/*func SetGithubCredentials(id, secret string) {
	//githubCred = "client_id=" + id + "&client_secret=" + secret
}*/

func SetGithubCredentials(token string) {
	if len(token) > 0 {
		githubCred = "access_token=" + token
	}
}

// GetGithubDoc downloads tarball from github.com.
func GetGithubDoc(client *http.Client, match map[string]string, installGOPATH string, node *Node, cmdFlags map[string]bool) ([]string, error) {
	match["cred"] = githubCred

	// JSON struct for github.com.
	var refs []*struct {
		Ref    string
		Url    string
		Object struct {
			Sha  string
			Type string
			Url  string
		}
	}

	// bundle and snapshot will have commit 'B' and 'S',
	// but does not need to download dependencies.
	isCheckImport := len(node.Value) == 0

	switch {
	case isCheckImport || len(node.Value) == 1:
		// Get up-to-date version.
		err := httpGetJSON(client, expand("https://api.github.com/repos/{owner}/{repo}/git/refs?{cred}", match), &refs)
		if err != nil {
			return nil, err
		}

		tags := make(map[string]string)
		for _, ref := range refs {
			switch {
			case strings.HasPrefix(ref.Ref, "refs/heads/"):
				tags[ref.Ref[len("refs/heads/"):]] = ref.Object.Sha
			case strings.HasPrefix(ref.Ref, "refs/tags/"):
				tags[ref.Ref[len("refs/tags/"):]] = ref.Object.Sha
			}
		}

		// Check revision tag.
		match["tag"], match["sha"], err = bestTag(tags, "master")
		if err != nil {
			return nil, err
		}

		node.Type = "commit"
		node.Value = match["sha"]
	case !isCheckImport: // Bundle or snapshot.
		// Check downlaod type.
		switch node.Type {
		case "tag", "commit", "branch":
			match["sha"] = node.Value
		default:
			return nil, errors.New("Unknown node type: " + node.Type)
		}
	}

	// We use .zip here.
	// zip : https://github.com/{owner}/{repo}/archive/{sha}.zip
	// tarball : https://github.com/{owner}/{repo}/tarball/{sha}

	// Downlaod archive.
	p, err := HttpGetBytes(client, expand("https://github.com/{owner}/{repo}/archive/{sha}.zip", match), nil)
	if err != nil {
		return nil, err
	}

	shaName := expand("{repo}-{sha}", match)
	if node.Type == "tag" {
		shaName = strings.Replace(shaName, "-v", "-", 1)
	}

	projectPath := expand("github.com/{owner}/{repo}", match)
	installPath := installGOPATH + "/src/" + projectPath
	node.ImportPath = projectPath

	// Remove old files.
	os.RemoveAll(installPath + "/")
	// Create destination directory.
	os.MkdirAll(installPath+"/", os.ModePerm)

	r, err := zip.NewReader(bytes.NewReader(p), int64(len(p)))
	if err != nil {
		return nil, err
	}

	isCodeOnly := cmdFlags["-c"]
	dirs := make([]string, 0, 5)
	// Need to add root path because we cannot get from tarball.
	dirs = append(dirs, installPath+"/")
	for _, f := range r.File {
		absPath := strings.Replace(f.FileInfo().Name(), shaName, installPath, 1)
		// Create diretory before create file.
		os.MkdirAll(path.Dir(absPath)+"/", os.ModePerm)

	compareDir:
		switch {
		case strings.HasSuffix(absPath, "/"): // Directory.
			// Check if current directory is example.
			if !(!cmdFlags["-e"] && strings.Contains(absPath, "example")) {
				for _, d := range dirs {
					if d == absPath {
						break compareDir
					}
				}
				dirs = append(dirs, absPath)
			}
		case isCodeOnly && !utils.IsDocFile(path.Base(absPath)):
			continue
		case !strings.HasPrefix(f.FileInfo().Name(), "."):
			// Get file from archive.
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}

			// Write data to file
			fw, _ := os.Create(absPath)
			if err != nil {
				return nil, err
			}

			_, err = io.Copy(fw, rc)
			// Close files.
			rc.Close()
			fw.Close()
			if err != nil {
				return nil, err
			}

			// Set modify time.
			os.Chtimes(absPath, f.ModTime(), f.ModTime())
		}
	}

	var imports []string

	// Check if need to check imports.
	if isCheckImport {
		for _, d := range dirs {
			importPkgs, err := CheckImports(d, match["importPath"])
			if err != nil {
				return nil, err
			}
			imports = append(imports, importPkgs...)
		}
	}

	/*fpath := appPath + "repo/tarballs/" + node.ImportPath + "-" + node.Value + ".zip"
	// Save tarball.
	if autoBackup && !utils.IsExist(fpath) {
		os.MkdirAll(path.Dir(fpath)+"/", os.ModePerm)
		f, err := os.Create(fpath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		_, err = f.Write(p)
	}*/

	return imports, err
}
