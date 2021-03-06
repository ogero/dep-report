package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/pkg/errors"
)

var client = &http.Client{Timeout: 5 * time.Second}
var licenseForRepo = map[string]string{
	"golang.org/x/crypto":   "BSD-3-Clause",
	"golang.org/x/sync":     "BSD-3-Clause",
	"golang.org/x/image":    "BSD-3-Clause",
	"golang.org/x/net":      "BSD-3-Clause",
	"golang.org/x/sys":      "BSD-3-Clause",
	"golang.org/x/text":     "BSD-3-Clause",
	"golang.org/x/tools":    "BSD-3-Clause",
	"golang.org/x/xerrors":  "BSD-3-Clause",
	"golang.org/x/oauth2":   "BSD-3-Clause",
	"google.golang.org/api": "BSD-3-Clause",
	"cloud.google.com/go":   "NOASSERTION",
}

var repoURLForPackage = map[string]string{
	"go.opencensus.io":            "https://github.com/census-instrumentation/opencensus-go",
	"google.golang.org/grpc":      "https://github.com/grpc/grpc-go",
	"google.golang.org/genproto":  "https://github.com/googleapis/go-genproto",
	"google.golang.org/appengine": "https://github.com/golang/appengine",
	"google.golang.org/api":       "https://code-review.googlesource.com/projects/google-api-go-client",
	"cloud.google.com/go":         "https://code-review.googlesource.com/projects/gocloud",
}

const (
	GITHUB = "github"
	GITLAB = "gitlab"
	GERRIT = "gerrit"
)

// Objects used when reading from Gopkg.lock
type pkgObject struct {
	Source   string
	Name     string
	VCS      string
	Revision string
	Branch   string
}

type pkg struct {
	Projects []pkgObject
}

// Objects used in construction of report
type versionDetails struct {
	Version string `json:"version,omitempty"`
	Time    string `json:"time"`
	Commit  string `json:"commit"`
}

type reportObject struct {
	Name      string         `json:"name"`
	Source    string         `json:"source"`
	License   string         `json:"license"`
	Website   string         `json:"website"`
	Installed versionDetails `json:"installed"`
	Latest    versionDetails `json:"latest"`
}

type report struct {
	Product      string         `json:"product"`
	ReportTime   string         `json:"reportTime"`
	Commit       string         `json:"commit"`
	CommitTime   string         `json:"commitTime"`
	Dependencies []reportObject `json:"dependencies"`
}

func main() {
	githubToken := os.Getenv("GITHUB_OAUTH_TOKEN")
	if githubToken == "" {
		log.Fatal("missing argument: GitHub Token")
	}

	productName, ok := os.LookupEnv("DEP_REPORT_PRODUCT")
	if !ok {
		productName = "b5server"
	}

	pkg, err := readGopkg()
	if err != nil {
		log.Fatal(err)
	}

	commit, commitTime := getCurrentCommitAndCommitTime()

	report := report{
		Product:    productName,
		Commit:     commit,
		CommitTime: commitTime,
		ReportTime: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}

	for _, pObj := range pkg.Projects {
		rObj, err := reportObjFromPkgObj(pObj, githubToken)
		if err != nil {
			log.Fatal("Failed to get report object from pkg object", pObj, err)
		}

		report.Dependencies = append(report.Dependencies, rObj)
	}

	prettyReport, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatal("Failed to json.MarshalIndent", err)
	}

	fmt.Println(string(prettyReport))
}

func readGopkg() (*pkg, error) {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal("Failed to get working directory", err)
	}

	pkgData, err := ioutil.ReadFile(wd + "/Gopkg.lock")
	if err != nil {
		return nil, errors.New("Failed to read" + wd + "/Gopkg.lock" + err.Error())
	}

	var pkg pkg
	if _, err := toml.Decode(string(pkgData), &pkg); err != nil {
		return nil, errors.New("Failed to json.Unmarshal pkg data" + err.Error())
	}

	return &pkg, nil
}

func getCurrentCommitAndCommitTime() (string, string) {
	commitBytes, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		log.Fatal("Failed to get current commit", err)
	}
	commit := strings.TrimSpace(string(commitBytes))

	commitTimeBytes, err := exec.Command("git", "show", "-s", "--format=%cI", "HEAD").Output()
	if err != nil {
		log.Fatal("Failed to get current commit time", err)
	}
	commitTime := strings.TrimSpace(string(commitTimeBytes))

	return commit, commitTime
}

type license struct {
	Name string `json:"spdx_id"`
}

type licenseResponse struct {
	License license `json:"license"`
}

type committer struct {
	Date string `json:"date"`
}

type commit struct {
	Committer committer `json:"committer"`
}

type commitResponse struct {
	Commit commit `json:"commit"`
	SHA    string `json:"sha"`
}

type branchInfo struct {
	Revision string `json:"revision"`
}

type release struct {
	Name string `json:"tag_name"`
}

type tag struct {
	Ref string `json:"ref"`
}

func reportObjFromPkgObj(m pkgObject, githubToken string) (reportObject, error) {
	log.Println("Transforming ", m.Name)
	r := reportObject{
		Name:    m.Name,
		Website: m.Source,
	}

	source := determineSource(m.Name)

	switch source {
	case GITHUB:
		if err := reportObjFromGithub(&r, m, githubToken); err != nil {
			return r, err
		}
	case GITLAB:
		if err := reportObjFromGitlab(&r, m); err != nil {
			return r, err
		}
	case GERRIT:
		if err := reportObjFromGerrit(&r, m); err != nil {
			return r, err
		}
	default:
		log.Println("Unable to determine repo source for ", m.Name)
	}

	return r, nil
}

func determineSource(packageName string) string {
	repo := packageName

	if url, ok := repoURLForPackage[packageName]; ok {
		repo = url
	}

	if strings.Contains(repo, GITHUB) {
		return GITHUB
	}

	if strings.Contains(repo, "1password.io") {
		return GITLAB
	}

	if strings.Contains(repo, "googlesource") {
		return GERRIT
	}

	if strings.Contains(repo, "golang.org/x") {
		return GERRIT
	}

	return ""
}

func reportObjFromGitlab(r *reportObject, m pkgObject) error {
	r.Source = "gitlab"
	// TODO
	return nil
}

func reportObjFromGithub(r *reportObject, m pkgObject, githubToken string) error {
	r.Source = "github"
	r.Name = repoNameFromGithubPackage(m.Name)
	repoURL := "https://api.github.com/repos/" + r.Name
	r.Website = repoURL

	licenseURL := repoURL + "/license"
	var licenseResponse licenseResponse
	if err := getGithub(licenseURL, &licenseResponse, githubToken); err != nil {
		return errors.Wrapf(err, "Unable to get from %s :", licenseURL)
	}

	r.License = licenseResponse.License.Name

	commitURL := repoURL + "/commits/" + m.Revision
	var installed commitResponse
	if err := getGithub(commitURL, &installed, githubToken); err != nil {
		return errors.Wrapf(err, "Unable to get from %s :", commitURL)
	}

	r.Installed = versionDetails{
		Commit: m.Revision,
		Time:   installed.Commit.Committer.Date,
	}

	branchURL := repoURL + "/commits/HEAD"
	var latest commitResponse
	if err := getGithub(branchURL, &latest, githubToken); err != nil {
		return errors.Wrapf(err, "Unable to get from %s :", branchURL)
	}

	r.Latest = versionDetails{
		Commit: latest.SHA,
		Time:   latest.Commit.Committer.Date,
	}

	releaseURL := repoURL + "/releases/latest"
	var release release
	if err := getGithub(releaseURL, &release, githubToken); err != nil {
		return errors.Wrapf(err, "Unable to get from %s :", releaseURL)
	}
	r.Latest.Version = release.Name

	return nil
}

func repoNameFromGithubPackage(packageName string) string {
	if url, found := repoURLForPackage[packageName]; found {
		packageName = url
	}

	parts := strings.Split(packageName, "/")
	if len(parts) > 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	} else {
		return packageName
	}

}

func getGithub(url string, target interface{}, token string) error {
	req, err := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", "token "+token)
	r, err := client.Do(req)
	if err != nil {
		return errors.Wrapf(err, "Unable to client.Do")
	}
	defer r.Body.Close()

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return errors.Wrapf(err, "Unable to ioutil.ReadAll")
	}

	err = json.Unmarshal(body, target)
	if err != nil {
		return errors.Wrapf(err, "Unable to json.Unmarshal")
	}

	return nil
}

func reportObjFromGerrit(r *reportObject, m pkgObject) error {
	r.Source = "gerrit"
	r.Name = m.Name

	var repoURL string
	if url, found := repoURLForPackage[r.Name]; found {
		repoURL = url
	} else {
		repoName := strings.TrimPrefix(r.Name, "golang.org/x/")
		repoURL = "https://go-review.googlesource.com/projects/" + repoName
	}
	r.Website = repoURL

	commitURL := repoURL + "/commits/" + m.Revision
	var installed commit
	if err := getGerrit(commitURL, &installed); err != nil {
		return errors.Wrapf(err, "Unable to get from %s :", commitURL)
	}

	t, err := formatGerritTime(installed.Committer.Date)
	if err != nil {
		return errors.Wrapf(err, "Unable to formatGerritTime")
	}
	r.Installed = versionDetails{
		Commit: m.Revision,
		Time:   t,
	}

	masterURL := repoURL + "/branches/master"
	var masterInfo branchInfo
	if err := getGerrit(masterURL, &masterInfo); err != nil {
		return errors.Wrapf(err, "Unable to get from %s :", masterURL)
	}

	latestURL := repoURL + "/commits/" + masterInfo.Revision
	var latest commit
	if err := getGerrit(latestURL, &latest); err != nil {
		return errors.Wrapf(err, "Unable to get from %s :", latestURL)
	}

	t, err = formatGerritTime(latest.Committer.Date)
	if err != nil {
		return errors.Wrapf(err, "Unable to formatGerritTime")
	}
	r.Latest = versionDetails{
		Commit: masterInfo.Revision,
		Time:   t,
	}

	tagsURL := repoURL + "/tags"
	tags := []tag{}
	if err := getGerrit(tagsURL, &tags); err != nil {
		return errors.Wrapf(err, "Unable to get from %s :", tagsURL)
	}
	if len(tags) > 0 {
		lastTag := tags[len(tags)-1]
		r.Latest.Version = strings.Replace(lastTag.Ref, "refs/tags/", "", 1)
	}

	var ok bool
	r.License, ok = licenseForRepo[m.Name]
	if !ok {
		return fmt.Errorf("License info for %s not provided", m.Name)
	}

	return nil
}

func formatGerritTime(t string) (string, error) {
	t1, err := time.Parse("2006-01-02 15:04:05.999999999", t)
	if err != nil {
		return "", errors.Wrapf(err, "Unable to parse Gerrit time")
	}
	return t1.Format("2006-01-02T15:04:05Z"), nil
}

func getGerrit(url string, target interface{}) error {
	req, err := http.NewRequest("GET", url, nil)

	r, err := client.Do(req)
	if err != nil {
		return errors.Wrapf(err, "Unable to client.Do")
	}
	defer r.Body.Close()

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return errors.Wrapf(err, "Unable to ioutil.ReadAll")
	}

	// Gerrit REST API appends a magic string before json body which needs to be removed
	// https://gerrit-review.googlesource.com/Documentation/rest-api.html#output
	bodyString := strings.Replace(string(body), ")]}'\n", "", 1)

	err = json.Unmarshal([]byte(bodyString), target)
	if err != nil {
		return errors.Wrapf(err, "Unable to json.Unmarshal")
	}

	return nil
}
