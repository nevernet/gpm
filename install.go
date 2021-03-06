// Copyright (c) 2013 GPMGo Members. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/GPMGo/gpm/doc"
	"github.com/GPMGo/gpm/utils"
)

var (
	isHasGit, isHasHg bool
	downloadCache     map[string]bool // Saves packages that have been downloaded.
	installGOPATH     string          // The GOPATH that packages are downloaded to.
)

var cmdInstall = &Command{
	UsageLine: "install [install flags] <packages|bundles|snapshots>",
}

func init() {
	downloadCache = make(map[string]bool)
	cmdInstall.Run = runInstall
	cmdInstall.Flags = map[string]bool{
		"-p": false,
		"-c": false,
		"-d": false,
		"-u": false, // Flag for 'go get'.
		"-e": false,
		"-s": false,
	}
}

// printInstallPrompt prints prompt information to users to
// let them know what's going on.
func printInstallPrompt(flag string) {
	switch flag {
	case "-p":
		fmt.Printf(fmt.Sprintf("%s\n", promptMsg["PureDownload"]))
	case "-c":
		fmt.Printf(fmt.Sprintf("%s\n", promptMsg["PureCode"]))
	case "-d":
		fmt.Printf(fmt.Sprintf("%s\n", promptMsg["DownloadOnly"]))
	case "-e":
		fmt.Printf(fmt.Sprintf("%s\n", promptMsg["DownloadExDeps"]))
	case "-s":
		fmt.Printf(fmt.Sprintf("%s\n", promptMsg["DownloadFromSrcs"]))
	}
}

// checkFlags checks if the flag exists with correct format.
func checkFlags(flags map[string]bool, enable []string, args []string, print func(string)) int {
	// Check auto-enable.
	for _, v := range enable {
		flags["-"+v] = true
		print("-" + v)
	}

	num := 0 // Number of valid flags, use to cut out.
	for i, f := range args {
		// Check flag prefix '-'.
		if !strings.HasPrefix(f, "-") {
			// Not a flag, finish check process.
			break
		}

		// Check if it a valid flag.
		if v, ok := flags[f]; ok {
			flags[f] = !v
			if !v {
				print(f)
			} else {
				fmt.Println("DISABLE: " + f)
			}
		} else {
			fmt.Printf(fmt.Sprintf("%s\n", promptMsg["UnknownFlag"]), f)
			return -1
		}
		num = i + 1
	}

	return num
}

// checkVCSTool checks if users have installed version control tools.
func checkVCSTool() {
	// git.
	if _, err := exec.LookPath("git"); err == nil {
		isHasGit = true
	}
	// hg.
	if _, err := exec.LookPath("hg"); err == nil {
		isHasHg = true
	}
	// svn.
}

func runInstall(cmd *Command, args []string) {
	// Check flags.
	num := checkFlags(cmd.Flags, config.AutoEnable.Install, args, printInstallPrompt)
	if num == -1 {
		return
	}
	args = args[num:]

	// Check length of arguments.
	if len(args) < 1 {
		fmt.Printf(fmt.Sprintf("%s\n", promptMsg["NoPackage"]))
		return
	}

	// Check version control tools.
	checkVCSTool()

	installGOPATH = utils.GetBestMatchGOPATH(appPath)
	fmt.Printf(fmt.Sprintf("%s\n", promptMsg["DownloadPath"]), installGOPATH)

	// Generate temporary nodes.
	nodes := make([]*doc.Node, len(args))
	for i := range nodes {
		nodes[i] = new(doc.Node)
		nodes[i].ImportPath = args[i]
	}
	// Download packages.
	downloadPackages(nodes)

	if !cmdInstall.Flags["-d"] && cmdInstall.Flags["-p"] {
		// Install packages all together.
		var cmdArgs []string
		cmdArgs = append(cmdArgs, "install")
		cmdArgs = append(cmdArgs, "<blank>")

		paths := utils.GetGOPATH()
		pkgPath := "/pkg/" + runtime.GOOS + "_" + runtime.GOARCH + "/"
		for k := range downloadCache {
			// Delete old packages.
			for _, p := range paths {
				os.RemoveAll(p + pkgPath + k + "/")
				os.Remove(p + pkgPath + k + ".a")
			}
		}

		for k := range downloadCache {
			fmt.Printf(fmt.Sprintf("%s\n", promptMsg["InstallStatus"]), k)
			cmdArgs[1] = k
			executeCommand("go", cmdArgs)
		}

		// Save local nodes to file.
		fw, err := os.Create(appPath + "data/nodes.json")
		if err != nil {
			fmt.Printf(fmt.Sprintf("ERROR: runInstall -> %s\n", promptMsg["OpenFile"]), err)
			return
		}
		defer fw.Close()
		fbytes, err := json.MarshalIndent(&localNodes, "", "\t")
		if err != nil {
			fmt.Printf(fmt.Sprintf("ERROR: runInstall -> %s\n", promptMsg["ParseJSON"]), err)
			return
		}
		fw.Write(fbytes)
	}
}

// chekcDeps checks dependencies of nodes.
func chekcDeps(nodes []*doc.Node) (depnodes []*doc.Node) {
	for _, n := range nodes {
		// Make sure it will not download all dependencies automatically.
		if len(n.Value) == 0 {
			n.Value = "B"
		}
		depnodes = append(depnodes, n)
		depnodes = append(depnodes, chekcDeps(n.Deps)...)
	}
	return depnodes
}

// checkLocalBundles checks if the bundle is in local file system.
func checkLocalBundles(bundle string) (nodes []*doc.Node) {
	for _, b := range localBundles {
		if bundle == b.Name {
			nodes = append(nodes, chekcDeps(b.Nodes)...)
			return nodes
		}
	}
	return nil
}

// downloadPackages downloads packages with certain commit,
// if the commit is empty string, then it downloads all dependencies,
// otherwise, it only downloada package with specific commit only.
func downloadPackages(nodes []*doc.Node) {
	// Check all packages, they may be bundles, snapshots or raw packages path.
	for _, n := range nodes {
		// Check if it is a bundle or snapshot.
		switch {
		case strings.HasSuffix(n.ImportPath, ".b"):
			l := len(n.ImportPath)
			// Check local bundles.
			bnodes := checkLocalBundles(n.ImportPath[:l-2])
			if len(bnodes) > 0 {
				// Check with users if continue.
				fmt.Printf(fmt.Sprintf("%s\n", promptMsg["BundleInfo"]), n.ImportPath[:l-2])
				for _, bn := range bnodes {
					fmt.Printf("[%s] -> %s: %s.\n", bn.ImportPath, bn.Type, bn.Value)
				}
				fmt.Printf(fmt.Sprintf("%s\n", promptMsg["ContinueDownload"]))
				var option string
				fmt.Fscan(os.Stdin, &option)
				if strings.ToLower(option) != "y" {
					os.Exit(0)
				}
				downloadPackages(bnodes)
			} else {
				// Check from server.
				// TODO: api.GetBundleInfo()
				fmt.Println("Unable to find bundle, and we cannot check with server right now.")
			}
		case strings.HasSuffix(n.ImportPath, ".s"):
			// TODO: api.GetSnapshotInfo()
		case utils.IsValidRemotePath(n.ImportPath):
			if !downloadCache[n.ImportPath] {
				// Download package.
				node, imports := downloadPackage(n)
				if len(imports) > 0 {
					// Need to download dependencies.
					// Generate temporary nodes.
					nodes := make([]*doc.Node, len(imports))
					for i := range nodes {
						nodes[i] = new(doc.Node)
						nodes[i].ImportPath = imports[i]
					}
					downloadPackages(nodes)
				}

				// Only save package information with specific commit.
				if node != nil {
					// Save record in local nodes.
					saveNode(node)
				}
			} else {
				fmt.Printf(fmt.Sprintf("%s\n", promptMsg["SkipDownloaded"]), n.ImportPath)
			}
		default:
			// Invalid import path.
			fmt.Printf(fmt.Sprintf("%s\n", promptMsg["SkipInvalidPath"]), n.ImportPath)
		}
	}
}

// saveNode saves node into local nodes.
func saveNode(n *doc.Node) {
	// Node dependencies list.
	n.Deps = nil

	// Check if this node exists.
	for i, v := range localNodes {
		if n.ImportPath == v.ImportPath {
			localNodes[i] = n
			return
		}
	}

	// Add new node.
	localNodes = append(localNodes, n)
}

// downloadPackage downloads package either use version control tools or not.
func downloadPackage(node *doc.Node) (*doc.Node, []string) {
	// Check if use version control tools.
	switch {
	case !cmdInstall.Flags["-p"] &&
		((node.ImportPath[0] == 'g' && isHasGit) || (node.ImportPath[0] == 'c' && isHasHg)): // github.com, code.google.com
		fmt.Printf(fmt.Sprintf("%s\n", promptMsg["InstallByGoGet"]), node.ImportPath)
		args := checkGoGetFlags()
		args = append(args, node.ImportPath)
		executeCommand("go", args)
		return nil, nil
	default: // Pure download.
		if !cmdInstall.Flags["-p"] {
			cmdInstall.Flags["-p"] = true
			fmt.Printf(fmt.Sprintf("%s\n", promptMsg["NoVCSTool"]))
		}

		fmt.Printf(fmt.Sprintf("%s\n", promptMsg["DownloadStatus"]), node.ImportPath)
		// Mark as donwloaded.
		downloadCache[node.ImportPath] = true

		imports, err := pureDownload(node)

		if err != nil {
			fmt.Printf(fmt.Sprintf("%s\n", promptMsg["DownloadError"]), node.ImportPath, err)
			return nil, nil
		}

		return node, imports
	}
}

func checkGoGetFlags() (args []string) {
	args = append(args, "get")
	switch {
	case cmdInstall.Flags["-d"]:
		args = append(args, "-d")
		fallthrough
	case cmdInstall.Flags["-u"]:
		args = append(args, "-u")
	}

	return args
}

// service represents a source code control service.
type service struct {
	pattern *regexp.Regexp
	prefix  string
	get     func(*http.Client, map[string]string, string, *doc.Node, map[string]bool) ([]string, error)
}

// services is the list of source code control services handled by gopkgdoc.
var services = []*service{
	{doc.GithubPattern, "github.com/", doc.GetGithubDoc},
	{doc.GooglePattern, "code.google.com/", doc.GetGoogleDoc},
	{doc.BitbucketPattern, "bitbucket.org/", doc.GetBitbucketDoc},
	{doc.LaunchpadPattern, "launchpad.net/", doc.GetLaunchpadDoc},
}

// pureDownload downloads package without version control.
func pureDownload(node *doc.Node) ([]string, error) {
	for _, s := range services {
		if s.get == nil || !strings.HasPrefix(node.ImportPath, s.prefix) {
			continue
		}
		m := s.pattern.FindStringSubmatch(node.ImportPath)
		if m == nil {
			if s.prefix != "" {
				return nil,
					doc.NotFoundError{fmt.Sprintf("%s", promptMsg["NotFoundError"])}
			}
			continue
		}
		match := map[string]string{"importPath": node.ImportPath}
		for i, n := range s.pattern.SubexpNames() {
			if n != "" {
				match[n] = m[i]
			}
		}
		return s.get(doc.HttpClient, match, installGOPATH, node, cmdInstall.Flags)
	}
	return nil, errors.New(fmt.Sprintf("%s", promptMsg["NotFoundError"]))
}
