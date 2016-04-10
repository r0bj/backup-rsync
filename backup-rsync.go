package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"io/ioutil"
	"sort"
	"path"
	"time"
	"regexp"
	"os/signal"
	"syscall"
	"path/filepath"

	log "github.com/Sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"github.com/nightlyone/lockfile"
)

const (
	logFile string = "/var/log/backup-rsync.log"
	lockFile string = "backup-rsync.lock"
	rsyncLogFilePattern string = "/var/log/backup-rsync"
	configFile string = "/etc/backup-rsync.yml"
	dateLayout string = "2006-01-02"
	defaultConcurrentRsync int = 3
	defaultRetentionDays int = 7
)

type Command struct {
	cmd string
	args []string
}

type Config struct {
	Root_dir interface{}
	Concurrent_rsync interface{} `yaml:",omitempty"`
	Retention_days interface{} `yaml:",omitempty"`
	Hosts []struct {
		Name string
		Limit_concurrent_rsync interface{} `yaml:",omitempty"`
		Retention_days interface{} `yaml:",omitempty"`
		Login_user interface{} `yaml:",omitempty"`
		Dirs []struct {
			Path string
			Retention_days interface{} `yaml:",omitempty"`
			Bandwidth_limit interface{} `yaml:",omitempty"`
		}
	}
}

type Path struct {
	host string
	path string
	retentionDays int
	concurrentRsyncLimit int
	bandwidthLimit interface{}
	loginUser interface{}
}

func worker(id int, jobs <-chan Command, results chan<- error) {
	for j := range jobs {
		cmd := fmt.Sprintf("%s %s", j.cmd, strings.Join(j.args, " "))
		log.Infof("Worker ID: %d; execute command: %s", id, cmd)
		if err := exec.Command(j.cmd, j.args...).Run(); err == nil {
			log.Infof("Worker ID: %d; command succesful: %s", id, cmd)
			results <- nil
		} else {
			log.Errorf("Worker ID: %d; command fail: %s: %s", id, cmd, err)
			results <- err
		}
	}
}

func parseYaml(file string) Config {
	source, err := ioutil.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}

	var c Config
	err = yaml.Unmarshal([]byte(source), &c)
	if err != nil {
		log.Fatal(err)
	}
	return c
}

func getPaths(config Config) []Path {
	var paths []Path
	for _, host := range config.Hosts {
		for _, dir := range host.Dirs {
			if dir.Path == "" {
				continue
			}
			var p Path
			p.host = host.Name
			p.path = dir.Path
			p.bandwidthLimit = dir.Bandwidth_limit
			p.loginUser = host.Login_user

			if dir.Retention_days != nil {
				p.retentionDays = dir.Retention_days.(int)
			} else if host.Retention_days != nil {
				p.retentionDays = host.Retention_days.(int)
			} else {
				p.retentionDays = config.Retention_days.(int)
			}

			if host.Limit_concurrent_rsync != nil {
				if host.Limit_concurrent_rsync.(int) > config.Concurrent_rsync.(int) {
					p.concurrentRsyncLimit = config.Concurrent_rsync.(int)
				} else {
					p.concurrentRsyncLimit = host.Limit_concurrent_rsync.(int)
				}
			} else {
				p.concurrentRsyncLimit = config.Concurrent_rsync.(int)
			}
			paths = append(paths, p)
		}
	}
	return paths	
}

// change paths order to avoid as much as possible multiple rsync to same host
// separate group of paths to the same host
func preparePathsOrder(paths []Path) []Path {
	m := make(map[string][]Path)
	for _, p := range paths {
		m[p.host] = append(m[p.host], p)
	}

	// prepare sorted keys to iterate by sorted map m
    var keys []string
    for k := range m {
        keys = append(keys, k)
    }
    sort.Strings(keys)

	var pathsSorted []Path
	for i := 0; i < len(paths); i++ {
		// iterate over sorted map keys
		for _, host := range keys {
			if len(m[host]) > 0 {
				pathsSorted = append(pathsSorted, m[host][0])
				m[host] = append(m[host][:0], m[host][1:]...)
			}
		}
	}
	return pathsSorted
}

func prepareCommands(paths []Path, config Config) []Command {
	cmds := make([]Command, 0)
	for _, p := range paths {
		var s Command
		s.cmd = "rsync"
		s.args = []string{
			"-avHAX",
			"--delete",
			"--backup",
			"--backup-dir=" + config.Root_dir.(string) + "/" + p.host + "/" + path.Base(p.path) + "/" + fmt.Sprint(time.Now().Format(dateLayout)),
			"--log-file=" + rsyncLogFilePattern + "." + p.host + ".log",
		}
		if p.bandwidthLimit != nil {
			s.args = append(s.args, fmt.Sprintf("--bwlimit=%d", p.bandwidthLimit.(int)))
		}
		if p.loginUser != nil {
			s.args = append(s.args, p.loginUser.(string) + "@" + p.host + ":" + p.path + "/")
		} else {
			s.args = append(s.args, p.host + ":" + p.path + "/")
		}
		s.args = append(s.args, config.Root_dir.(string) + "/" + p.host + "/" + path.Base(p.path) + "/current/")

		cmds = append(cmds, s)
	}
	return cmds
}

func createTargetDirs(paths []Path, config Config) {
	for _, p := range paths {
		path := config.Root_dir.(string) + "/" + p.host + "/" + path.Base(p.path) + "/current"
		if _, err := os.Stat(path); os.IsNotExist(err) {
			log.Infof("Create directory %s", path)
			os.MkdirAll(path, 0644)
		}
	}
}

func deleteExpiredBackups(paths []Path, config Config) {
	for _, p := range paths {
		path := config.Root_dir.(string) + "/" + p.host + "/" + path.Base(p.path)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			items, _ := ioutil.ReadDir(path)
			for _, f := range items {
				t, err := time.Parse(dateLayout, f.Name())
				if err == nil {
					currentPath := path + "/" + f.Name()
					if time.Now().Unix() - t.Unix() > int64(p.retentionDays) * 86400 {
						log.Infof("Expired backup, deleting directory %s", currentPath)
						err := os.RemoveAll(currentPath)
						if err != nil {
							log.Errorf("Deleting directory %s failed", currentPath)
						}
					}
				}
			}
		}
	}
}

func executeWorkers(cmds []Command, config Config) {
	jobs := make(chan Command, len(cmds))
	results := make(chan error, len(cmds))

	for w := 1; w <= config.Concurrent_rsync.(int); w++ {
		go worker(w, jobs, results)
	}

	for _, j := range cmds {
		jobs <- j
	}
	close(jobs)

	for a := 1; a <= len(cmds); a++ {
		<-results
	}
}

func validatePaths(paths []Path, config *Config) []Path {
	r := regexp.MustCompile(`/$`)
	config.Root_dir = r.ReplaceAllString(config.Root_dir.(string), "")

	for i, _ := range paths {
		paths[i].path = r.ReplaceAllString(paths[i].path, "")
	}

	// downward loop to have ability to delete slice element while iterating 
	for i := len(paths) - 1; i >= 0; i-- {		
		if match, _ := regexp.MatchString(`^[\.]{1,2}$`, paths[i].path); match {
			paths = append(paths[:i], paths[i+1:]...)
		} 
	}
	return paths
}

func validateParams(config *Config) {
	if config.Concurrent_rsync == nil {
		config.Concurrent_rsync = defaultConcurrentRsync
	}
	if config.Retention_days == nil {
		config.Retention_days = defaultRetentionDays
	}
	if config.Root_dir == nil {
		log.Fatal("Cannot find root_dir key in config root level")
	}
}

func main() {
	f, err := os.OpenFile(logFile, os.O_APPEND | os.O_CREATE | os.O_WRONLY, 0644)
	if err != nil {
	    panic(err)
	}
	defer f.Close()
	log.SetOutput(f)
	log.SetLevel(log.DebugLevel)
	log.SetFormatter(&log.TextFormatter{DisableColors: true})


	lock, err := lockfile.New(filepath.Join(os.TempDir(), lockFile))
	if err != nil {
		log.Fatalf("Cannot init lock. reason: %v", err)
	}

	err = lock.TryLock()
	if err != nil {
		log.Fatalf("Cannot lock \"%v\", reason: %v", lock, err)
	}
	defer lock.Unlock()

	go func() {
		sigchan := make(chan os.Signal, 1)
		signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)
		<-sigchan
		log.Error("Program killed !")
		lock.Unlock()
		os.Exit(1)
	}()

	config := parseYaml(configFile)
	validateParams(&config)

	paths := preparePathsOrder(validatePaths(getPaths(config), &config))
	cmds := prepareCommands(paths, config)

	createTargetDirs(paths, config)
	executeWorkers(cmds, config)
	deleteExpiredBackups(paths, config)
}
