package main

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	lz4 "github.com/cloudflare/golz4"
	"github.com/slugalisk/overrustlelogs/common"
)

var commands = map[string]command{
	"compress":      compress,
	"uncompress":    uncompress,
	"read":          read,
	"readnicks":     readNicks,
	"nicks":         nicks,
	"migrate":       migrate,
	"namechange":    namechange,
	"cleanup":       cleanup,
	"convert":       convertToZSTD,
	"createtoplist": createTopList,
}

func main() {
	log.SetFlags(log.Lshortfile | log.LstdFlags)
	if len(os.Args) < 2 {
		os.Exit(1)
	}
	if c, ok := commands[os.Args[1]]; ok {
		if err := c(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	} else {
		fmt.Println("invalid command")
		os.Exit(1)
	}
	os.Exit(0)
}

type command func() error

func compress() error {
	if len(os.Args) < 3 {
		return errors.New("not enough args")
	}
	path := os.Args[2]
	_, err := common.CompressFile(path)
	return err
}

func uncompress() error {
	if len(os.Args) < 3 {
		return errors.New("not enough args")
	}
	path := os.Args[2]
	_, err := common.UncompressFile(path)
	return err
}

func nicks() error {
	if len(os.Args) < 3 {
		return errors.New("not enough args")
	}
	path := os.Args[2]
	var data []byte
	data, err := common.ReadCompressedFile(path)
	if os.IsNotExist(err) {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		data, err = ioutil.ReadAll(f)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	r := bufio.NewReaderSize(bytes.NewReader(data), len(data))
	nick := regexp.MustCompile("^\\[[^\\]]+\\]\\s*([a-zA-Z0-9\\_\\-]+):")
	nicks := common.NickList{}
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
		if ok := nick.Match(line); ok {
			match := nick.FindSubmatch(line)
			nicks.Add(string(match[1]))
		}
	}
	return nicks.WriteTo(regexp.MustCompile("\\.txt(\\.gz)?$").ReplaceAllString(path, ".nicks"))
}

func read() error {
	if len(os.Args) < 3 {
		return errors.New("not enough args")
	}
	path := os.Args[2]
	if regexp.MustCompile("\\.txt\\.gz$").MatchString(path) {
		buf, err := common.ReadCompressedFile(path)
		if err != nil {
			return err
		}
		os.Stdout.Write(buf)
	} else {
		return errors.New("invalid file")
	}
	return nil
}

func readNicks() error {
	if len(os.Args) < 3 {
		return errors.New("not enough args")
	}
	path := os.Args[2]
	if regexp.MustCompile("\\.nicks\\.gz$").MatchString(path) {
		nicks := common.NickList{}
		if err := common.ReadNickList(nicks, path); err != nil {
			return err
		}
		for nick := range nicks {
			fmt.Println(nick)
		}
	} else {
		return errors.New("invalid file")
	}
	return nil
}

func namechange() error {
	if len(os.Args) < 5 {
		return errors.New("not enough args")
	}
	validNick := regexp.MustCompile("^[a-zA-Z0-9_]+$")
	log := os.Args[2]
	oldName := os.Args[3]
	if !validNick.Match([]byte(oldName)) {
		return errors.New("the old name is not a valid nick")
	}
	newName := os.Args[4]

	replacer := strings.NewReplacer(
		"] "+oldName+":", "] "+newName+":",
		" "+oldName+" ", " "+newName+" ",
		" "+oldName+"\n", " "+newName+"\n",
	)

	log = strings.Replace(log, "txt", "nicks", 1)

	if strings.Contains(log, time.Now().UTC().Format("2006-01-02")) {
		return errors.New("can't modify todays log file")
	}
	fmt.Println(log)

	n := common.NickList{}
	err := common.ReadNickList(n, log)
	if err != nil {
		fmt.Println(err)
		return err
	}

	if _, ok := n[newName]; ok {
		return errors.New("nick already used, choose another one")
	}
	if _, ok := n[oldName]; !ok {
		return errors.New("nick not found")
	}
	n.Remove(oldName)
	n.Add(newName)
	err = n.WriteTo(log[:len(log)-4])
	if err != nil {
		fmt.Println(err)
		return err
	}

	log = strings.Replace(log, "nicks", "txt", 1)

	d, err := common.ReadCompressedFile(log)
	if err != nil {
		fmt.Println(err)
		return err
	}

	newData := []byte(replacer.Replace(string(d)))
	f, err := common.WriteCompressedFile(log, newData)
	if err != nil {
		fmt.Println(err)
		return err
	}
	fmt.Println("replaced nicks in", f.Name())
	f.Close()
	return nil
}

func cleanup() error {
	now := time.Now()

	logsPath := os.Args[2]

	filepaths, err := filepath.Glob(filepath.Join(logsPath, "/*/*/*"))
	if err != nil {
		log.Printf("error getting filepaths: %v", err)
		return err
	}
	log.Printf("found %d files, starting cleanup...", len(filepaths))

	r := regexp.MustCompile(`\.gz$`)

	for _, fp := range filepaths {
		if r.MatchString(fp) || strings.Contains(fp, now.Format("2006-01-02")) {
			continue
		}
		_, err := common.CompressFile(fp)
		if err != nil {
			log.Panicf("error writing compressed file: %v", err)
		}
		log.Println("compressed", fp)
	}
	return nil
}

func convertToZSTD() error {
	logsPath := os.Args[2]

	filepaths, err := filepath.Glob(filepath.Join(logsPath, "/*/*/*"))
	if err != nil {
		log.Printf("error getting filepaths: %v", err)
		return err
	}
	log.Printf("found %d files, starting cleanup...", len(filepaths))
	// now := time.Now().UTC()
	for _, fp := range filepaths {
		if strings.HasSuffix(fp, ".lz4") { //!strings.Contains(fp, now.Format("2006-01-02")) {
			data, err := UncompressFile(fp)
			if err != nil {
				log.Printf("error reading compressed file: %v", err)
			}
			data.Close()
			fp = fp[:len(fp)-4]
		}
		if strings.HasSuffix(fp, ".txt") || strings.HasSuffix(fp, ".nicks") {
			_, err = common.CompressFile(fp)
			if err != nil {
				log.Println(err)
			}
			// log.Println("compressed", fp)
			continue
		}
		// log.Println("error:", fp)
	}
	return nil
}

// ReadCompressedFile read compressed file
func ReadCompressedFile(path string) ([]byte, error) {
	f, err := os.Open(lz4Path(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}
	size := uint32(0)
	size |= uint32(c[0]) << 24
	size |= uint32(c[1]) << 16
	size |= uint32(c[2]) << 8
	size |= uint32(c[3])
	data := make([]byte, size)
	if err := lz4.Uncompress(c[4:], data); err != nil {
		return nil, err
	}
	return data, nil
}

// UncompressFile uncompress an existing file
func UncompressFile(path string) (*os.File, error) {
	d, err := ReadCompressedFile(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(strings.Replace(path, ".lz4", "", -1), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(d); err != nil {
		return nil, err
	}
	if err := os.Remove(lz4Path(path)); err != nil {
		return nil, err
	}
	return f, nil
}

func lz4Path(path string) string {
	if path[len(path)-4:] != ".lz4" {
		path += ".lz4"
	}
	return path
}

// ./tool createToplist /path/to/logs/ "September *"
func createTopList() error {

	month := os.Args[3]
	logsPath := os.Args[2]

	filepaths, err := filepath.Glob(filepath.Join(logsPath, "/*", month))
	if err != nil {
		log.Printf("error getting filepaths: %v", err)
		return err
	}

	for _, mpath := range filepaths {
		fmt.Printf("creating toplist for %s\n", mpath)
		toplist := make(map[string]*user)

		files, err := ioutil.ReadDir(mpath)
		if err != nil {
			fmt.Printf("error reading folder: %s with: %v", mpath, err)
			continue
		}

		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".txt.gz") {
				continue
			}
			b, err := common.ReadCompressedFile(filepath.Join(mpath, file.Name()))
			if err != nil {
				fmt.Printf("error: %v reading file %s", err, file.Name())
				continue
			}

			buf := bytes.NewBuffer(b)
			scanner := bufio.NewScanner(buf)

			for scanner.Scan() {
				line := scanner.Bytes()
				// some 2016 files are borked
				if !strings.HasPrefix(scanner.Text(), "[") {
					continue
				}

				endofdate := len("[2017-08-27 01:57:59 UTC] ")
				endofnick := bytes.Index(line[endofdate:], []byte(":"))

				nick := line[endofdate : endofnick+endofdate]
				date, err := time.Parse("[2006-01-02 15:04:05 MST] ", string(line[:endofdate]))
				if err != nil {
					fmt.Println(err)
				}

				if _, ok := toplist[string(nick)]; !ok {
					toplist[string(nick)] = &user{
						Lines:    1,
						Bytes:    len(line[endofnick+endofdate:]),
						Username: string(nick),
						Seen:     date.Unix(),
					}
					continue
				}

				toplist[string(nick)].Lines++
				toplist[string(nick)].Bytes += len(line[endofnick+endofdate:])
				if toplist[string(nick)].Seen < date.Unix() {
					toplist[string(nick)].Seen = date.Unix()
				}
			}

			if err := scanner.Err(); err != nil {
				fmt.Fprintln(os.Stderr, "reading standard input:", err)
			}
		}

		users := []*user{}
		for _, u := range toplist {
			users = append(users, u)
		}
		sort.Sort(ByLines(users))

		var buf bytes.Buffer
		err = gob.NewEncoder(&buf).Encode(users)
		if err != nil {
			fmt.Printf("error encoding users: %v", err)
			continue
		}

		_, err = common.WriteCompressedFile(filepath.Join(mpath, "toplist.json.gz"), buf.Bytes())
		if err != nil {
			fmt.Printf("error writing toplist file: %v", err)
		}
	}

	return nil
}

type user struct {
	Username string
	Lines    int
	Bytes    int
	Seen     int64
}

type ByLines []*user

func (a ByLines) Len() int           { return len(a) }
func (a ByLines) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByLines) Less(i, j int) bool { return a[i].Lines > a[j].Lines }
