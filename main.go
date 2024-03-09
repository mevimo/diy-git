package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"time"
)

var (
	NULL_SEP = []byte("\x00")
	SPACE    = []byte("\x20")
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mygit <command> [<args>...]\n")
		os.Exit(1)
	}

	command := os.Args[1]
	commandArgs := os.Args[2:]
	switch command {
	case "init":
		InitCmd(commandArgs)
	case "cat-file":
		CatFileCmd(commandArgs)
	case "hash-object":
		HashObjectCmd(commandArgs)
	case "ls-tree":
		LSTreeCmd(commandArgs)
	case "write-tree":
		WriteTreeCmd(commandArgs)
	case "commit-tree":
		CommitTreeCmd(commandArgs)
	case "commit":
		CommitCmd(commandArgs)
	case "clone":
		CloneCmd(commandArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", command)
		os.Exit(1)
	}
}

func InitCmd(args []string) {
	for _, dir := range []string{".git", ".git/objects", ".git/refs"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory: %s\n", err)
		}
	}

	headFileContents := []byte("ref: refs/heads/master\n")
	if err := os.WriteFile(".git/HEAD", headFileContents, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
	}

	fmt.Println("Initialized git directory")
}

func CatFileCmd(args []string) {
	if len(args) < 2 || args[0] != "-p" {
		fmt.Fprintf(os.Stderr, "Usage: mygit cat-file -p <blob_sha>\n")
		os.Exit(1)
	}

	CatFile(args[1], os.Stdout)
}

func CatFile(hashSum string, output io.Writer) {
	shaPrefix := hashSum[:2]
	shaAfterPrefix := hashSum[2:]

	filename := fmt.Sprintf(".git/objects/%s/%s", shaPrefix, shaAfterPrefix) // boldly assume cwd is where .git is
	file, err := os.Open(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not find Git object: %s\n", err)
		os.Exit(1)
	}

	reader, err := zlib.NewReader(file)
	defer reader.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decompressing Git object: %s\n", err)
		os.Exit(1)
	}
	breader := bufio.NewReader(reader)

	var currentByte byte = 1
	for currentByte != 0 {
		currentByte, err = breader.ReadByte()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading Git object: %s\n", err)
			os.Exit(1)
		}
	}

	if output != nil {
		io.Copy(output, breader)
	}
}

func HashObjectCmd(args []string) {
	if len(args) < 2 || args[0] != "-w" {
		fmt.Fprintf(os.Stderr, "Usage: mygit hash-object -w <file>\n")
		os.Exit(1)
	}

	hash := WriteBlob(args[1])
	fmt.Print(hex.EncodeToString(hash))
}

// Create a blob object for `filename`, return its SHA1.
func WriteBlob(filepath string) []byte {
	file, err := os.Open(filepath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening file: %s\n", err)
		os.Exit(1)
	}
	defer file.Close()

	fileinfo, err := file.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting file info: %s\n", err)
		os.Exit(1)
	}

	objectSize := strconv.FormatInt(fileinfo.Size(), 10)
	bbuf := bytes.NewBuffer([]byte("blob " + objectSize + "\x00"))

	_, err = io.Copy(bbuf, file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error copying file: %s\n", err)
		os.Exit(1)
	}

	hashSum := GetSha(bbuf.Bytes())
	WriteObject(hex.EncodeToString(hashSum), bbuf)
	return hashSum
}

func GetSha(buf []byte) []byte {
	hasher := sha1.New() // git actually uses a hardened version of sha-1, so this will not output the same hashes as git
	if _, err := hasher.Write(buf); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating hash: %s\n", err)
		os.Exit(1)
	}
	return hasher.Sum(nil)
}

// Will consume `contents` to write to an object file `name`.
func WriteObject(name string, contents io.Reader) {
	objectPath := ".git/objects/" + name[:2]
	err := os.MkdirAll(objectPath, 0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating object file: %s\n", err)
		os.Exit(1)
	}

	objectPath += "/" + name[2:]
	newFile, err := os.OpenFile(objectPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return // object already exists
		}
		fmt.Fprintf(os.Stderr, "Error creating object file: %s\n", err)
		os.Exit(1)
	}

	writer := zlib.NewWriter(newFile)
	defer writer.Close()
	io.Copy(writer, contents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating object file: %s\n", err)
		os.Exit(1)
	}
}

func LSTreeCmd(args []string) {
	if len(args) < 2 || args[0] != "--name-only" {
		fmt.Fprintf(os.Stderr, "Usage: mygit ls-tree --name-only <tree_sha>\n")
		os.Exit(1)
	}

	sha := args[1]
	shaPrefix := sha[:2]
	shaAfterPrefix := sha[2:]

	filename := fmt.Sprintf(".git/objects/%s/%s", shaPrefix, shaAfterPrefix) // boldly assume cwd is where .git is
	file, err := os.Open(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not find Git object: %s\n", err)
		os.Exit(1)
	}

	reader, err := zlib.NewReader(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decompressing Git object: %s\n", err)
		os.Exit(1)
	}
	buf, err := io.ReadAll(reader)
	reader.Close()

	_, body, _ := bytes.Cut(buf, NULL_SEP)
	var info []byte // otherwise I can only redefine the variable inside the loop scope which breaks the loop condition
	for len(body) > 0 {
		info, body, _ = bytes.Cut(body, NULL_SEP)
		_, fileName, _ := bytes.Cut(info, SPACE)
		fmt.Println(string(fileName))
		body = body[20:] // the '20' is the SHA1 hash
	}
}

type TreeFile struct {
	Name  string
	Mode  int
	IsDir bool
}

func WriteTreeCmd(args []string) {
	hash := WriteTree(".")
	fmt.Print(hex.EncodeToString(hash))
}

// Create a tree object for the directory `dir`, return its SHA1.
func WriteTree(dir string) []byte {
	files, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading current working directory: %s", err)
		os.Exit(1)
	}

	lines := []TreeFile{}
	for _, file := range files {
		fileinfo, err := file.Info()
		if fileinfo.Name() == ".git" {
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading current working directory: %s", err)
			os.Exit(1)
		}
		var mode int
		if fileinfo.Mode()&os.ModeSymlink != 0 {
			mode = 120000 // is symlink
		} else if fileinfo.Mode().Perm()%2 != 0 && !fileinfo.IsDir() {
			mode = 100755 // is executable (I just check 'others' bit I dont know what git does)
		} else if fileinfo.IsDir() {
			mode = 40000 // trees (040000)
		} else {
			mode = 100644 // regular file
		}
		lines = append(lines, TreeFile{
			Name:  fileinfo.Name(),
			Mode:  mode,
			IsDir: fileinfo.IsDir(),
		})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Name < lines[j].Name })

	body := bytes.NewBuffer([]byte{})
	for _, line := range lines {
		body.WriteString(fmt.Sprint(line.Mode))
		body.Write(SPACE)
		body.WriteString(line.Name)
		body.Write(NULL_SEP)

		path := dir + "/" + line.Name
		var hash []byte
		if line.IsDir {
			hash = WriteTree(path)
		} else {
			hash = []byte(WriteBlob(path))
		}
		body.Write(hash)
	}

	header := []byte(fmt.Sprintf("tree %s%s", fmt.Sprint(body.Len()), NULL_SEP))
	headerAndBody := bytes.NewBuffer(header)
	_, err = io.Copy(headerAndBody, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating tree object: %s", err)
		os.Exit(1)
	}

	hash := GetSha(headerAndBody.Bytes())
	WriteObject(hex.EncodeToString(hash), headerAndBody)
	return hash
}

func CommitTreeCmd(args []string) {
	if len(args) < 5 {
		fmt.Fprintf(os.Stderr, "Usage: mygit commit-tree <tree_sha> -p <commit_sha> -m <message>\n")
		os.Exit(1)
	}
	hash := CommitTree(args[0], args[2], args[4])
	fmt.Print(hex.EncodeToString(hash))
}

func CommitTree(treeSHA string, parentCommitSHA string, msg string) []byte {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	timezoneOffset := "+0100"
	author := "Florian Laporte"
	authorMail := "<florianl@florianl.dev>"

	body := bytes.NewBuffer(nil)
	body.WriteString(fmt.Sprintf("tree %s\nparent %s\n", treeSHA, parentCommitSHA))
	body.WriteString(fmt.Sprintf("author %s %s %s %s\n", author, authorMail, timestamp, timezoneOffset))
	body.WriteString(fmt.Sprintf("committer %s %s %s %s\n\n", author, authorMail, timestamp, timezoneOffset))
	body.WriteString(msg)
	body.WriteRune('\n')

	header := []byte(fmt.Sprintf("commit %s%s", fmt.Sprint(body.Len()), NULL_SEP))
	headerAndBody := bytes.NewBuffer(header)
	_, err := io.Copy(headerAndBody, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating commit object: %s", err)
		os.Exit(1)
	}

	hash := GetSha(headerAndBody.Bytes())
	WriteObject(hex.EncodeToString(hash), headerAndBody)
	return hash
}

func CommitCmd(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: mygit commit -m <message>\n")
		os.Exit(1)
	}

	hash := Commit(args[1])
	fmt.Print(hex.EncodeToString(hash))
}

// Commit the current working directory, return the commit SHA-1
func Commit(msg string) []byte {
	treeSHA := hex.EncodeToString(WriteTree("."))

	headFile, err := os.ReadFile(".git/HEAD")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading HEAD file: %s", err)
		os.Exit(1)
	}
	headRefPath := ".git/" + string(headFile[5:len(headFile)-1])
	HEADsha, err := os.ReadFile(headRefPath)
	HEADsha = HEADsha[:len(HEADsha)-1]
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving HEAD ref: %s", err)
		os.Exit(1)
	}

	hash := CommitTree(treeSHA, string(HEADsha), msg)
	return hash
}
