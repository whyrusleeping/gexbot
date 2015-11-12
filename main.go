package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	api "github.com/ipfs/go-ipfs-api"
	gx "github.com/whyrusleeping/gx/gxutil"
	hb "github.com/whyrusleeping/hellabot"
	. "github.com/whyrusleeping/stump"
)

const regname = "registry.json"
const maxPackageSize = 512000

type Package struct {
	Author string
	Hash   string
}

type Registry struct {
	lk   sync.Mutex
	pkgs map[string]*Package
	sh   *api.Shell
}

func (r *Registry) tryLoad(fname string) error {
	fi, err := os.Open(fname)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer fi.Close()

	return json.NewDecoder(fi).Decode(&r.pkgs)
}
func (r *Registry) addPackage(name string, pkg *Package) error {
	err := r.sh.Pin(pkg.Hash)
	r.lk.Lock()
	defer r.lk.Unlock()
	val, ok := r.pkgs[name]
	// TODO: other checks, like authorship and version increment
	if ok {
		err := r.sh.Unpin(val.Hash)
		if err != nil {
			Error("error unpinning old hash for %s - %s : %s", name, val, err)
		}
	}
	r.pkgs[name] = pkg
	err = r.writeToDisk(regname)
	if err != nil {
		return err
	}

	return nil
}

func (r *Registry) writeToDisk(fname string) error {
	fi, err := os.Create(fname)
	if err != nil {
		return err
	}
	defer fi.Close()

	return json.NewEncoder(fi).Encode(r.pkgs)
}

func (r *Registry) CheckAndAddPackage(name string, pkg *Package) error {
	elems, err := r.sh.List(pkg.Hash)
	if err != nil {
		return err
	}
	Log("package listed...")

	if len(elems) != 1 {
		return fmt.Errorf("expected just the package dir under given hash")
	}

	inpkg, err := r.sh.List(elems[0].Hash)
	if err != nil {
		return err
	}
	Log("package name: ", elems[0].Name)

	var pkgfile string
	for _, l := range inpkg {
		if l.Name == "package.json" {
			pkgfile = l.Hash
			break
		}
	}

	if pkgfile == "" {
		return fmt.Errorf("no package file found in given hash")
	}

	rc, err := r.sh.Cat(pkgfile)
	if err != nil {
		return err
	}
	defer rc.Close()

	var gxpkg gx.Package
	err = json.NewDecoder(rc).Decode(&gxpkg)
	if err != nil {
		return err
	}

	size, err := r.checkSize(pkg.Hash)
	if err != nil {
		return err
	}

	Log("package size: %d", size)
	if size > maxPackageSize {
		return fmt.Errorf("package too large! must be under %d bytes", maxPackageSize)
	}

	return r.addPackage(name, pkg)
}

func (r *Registry) checkSize(hash string) (int, error) {
	var sum int
	_, size, err := r.sh.BlockStat(hash)
	if err != nil {
		return 0, err
	}

	sum += size
	links, err := r.sh.List(hash)
	if err != nil {
		return 0, err
	}

	for _, l := range links {
		n, err := r.checkSize(l.Hash)
		if err != nil {
			return 0, err
		}

		sum += n
	}

	return sum, nil
}

func main() {
	sh := api.NewShell("localhost:5001")

	r := &Registry{
		pkgs: make(map[string]*Package),
		sh:   sh,
	}
	err := r.tryLoad(regname)
	if err != nil {
		Fatal(err)
	}

	pkgT := hb.Trigger{
		func(b *hb.Bot, mes *hb.Message) bool {
			return strings.HasPrefix(mes.Content, "!gx ")
		},
		func(c *hb.Bot, mes *hb.Message) bool {
			parts := strings.Split(mes.Content, " ")
			if len(parts) == 1 {
				return true
			}

			switch parts[1] {
			case "pub":
				if len(parts) != 4 {
					return true
				}

				name := parts[2]
				hash := parts[3]

				Log("add package request from %s [%s %s]", mes.From, name, hash)
				err := r.CheckAndAddPackage(name, &Package{
					Hash:   hash,
					Author: mes.From,
				})
				if err != nil {
					c.Msg(mes.To, err.Error())
				} else {
					c.Msg(mes.To, "success!")
				}

			default:
				Error("unrecognized command: ", parts[1])
			}

			return true
		},
	}

	bot, err := hb.NewBot("irc.freenode.com:6667", "gexbot", hb.ReconOpt())
	if err != nil {
		Fatal(err)
	}

	bot.Channels = []string{"#whydev"}

	bot.AddTrigger(pkgT)
	bot.Run()

	for range bot.Incoming {
	}

}
