package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/hashicorp/serf/serf"
	"golang.org/x/sync/errgroup"
)

type oneAndOnlyNumber struct {
	num        int
	generation int
	numMutex   sync.Mutex
}

func InitTheNumber(val int) *oneAndOnlyNumber {
	return &oneAndOnlyNumber{num: val}
}

func (n *oneAndOnlyNumber) setVal(val int) {
	n.numMutex.Lock()
	defer n.numMutex.Unlock()
	n.num = val
	n.generation++
}

func (n *oneAndOnlyNumber) getVal() (int, int) {
	n.numMutex.Lock()
	defer n.numMutex.Unlock()
	return n.num, n.generation
}

func (n *oneAndOnlyNumber) notifyValue(curVal int, curGeneration int) bool {
	if curGeneration > n.generation {
		n.numMutex.Lock()
		defer n.numMutex.Unlock()
		n.generation = curGeneration
		n.num = curVal
		return true
	}
	return false
}

func main() {
	advertiseAddr := os.Getenv("ADVERTISE_ADDR")
	clusterAddr := os.Getenv("CLUSTER_ADDR")
	log.Printf("advertise addr: %s, cluster addr: %s\n", advertiseAddr, clusterAddr)
	cluster, err := setupCluster(advertiseAddr, clusterAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer cluster.Leave()

	theOneAndOnlyNumber := InitTheNumber(42)
	go launchHTTPAPI(theOneAndOnlyNumber)
	ctx := context.Background()
	name, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	ctx = context.WithValue(ctx, "name", name)
	debugDataPrintTicker := time.Tick(time.Second * 5)
	numberBroadcastTicker := time.Tick(time.Second * 2)
	for {
		select {
		case <-numberBroadcastTicker:
			members := getOtherMembers(cluster)
			ctx, _ := context.WithTimeout(ctx, time.Second*2)
			log.Printf("%s: notify others: %d\n", name, len(members))
			go notifyOthers(ctx, members, theOneAndOnlyNumber)
		case <-debugDataPrintTicker:
			log.Printf("%s: members: %v\n", name, cluster.Members())
			curVal, curGen := theOneAndOnlyNumber.getVal()
			log.Printf("%s: state: val: %v gen: %v\n", name, curVal, curGen)
		}
	}
}

func getOtherMembers(cluster *serf.Serf) []serf.Member {
	members := cluster.Members()
	for i := 0; i < len(members); {
		if members[i].Name == cluster.LocalMember().Name || members[i].Status != serf.StatusAlive {
			if i < len(members)-1 {
				members = append(members[:i], members[i+1:]...)

			} else {
				members = members[:i]
			}
		} else {
			i++
		}
	}
	return members
}

func notifyOthers(ctx context.Context, otherMembers []serf.Member, db *oneAndOnlyNumber) {
	g, ctx := errgroup.WithContext(ctx)
	for _, member := range otherMembers {
		curMember := member
		g.Go(func() error {
			return notifyMember(ctx, curMember.Addr.String(), db)
		})
	}
	err := g.Wait()
	if err != nil {
		log.Printf("error when notify other members: %v", err)
	}
}

func notifyMember(ctx context.Context, addr string, db *oneAndOnlyNumber) error {
	val, gen := db.getVal()
	notifier := ctx.Value("name")
	log.Printf("%s: notify member: addr: %s val: %v gen: %v", notifier, addr, val, gen)
	req, err := http.NewRequest("POST",
		fmt.Sprintf("http://%v:8080/notify/%v/%v?notifier=%v", addr, val, gen), nil)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)
	_, err = http.DefaultClient.Do(req)
	return err
}

func setupCluster(advertiseAddr, clusterAddr string) (*serf.Serf, error) {
	conf := serf.DefaultConfig()
	conf.Init()
	conf.MemberlistConfig.AdvertiseAddr = advertiseAddr

	cluster, err := serf.Create(conf)
	if err != nil {
		return nil, err
	}

	if clusterAddr != "" {
		_, err = cluster.Join([]string{clusterAddr}, true)
		if err != nil {
			return nil, err
		}
	}

	return cluster, nil
}

func launchHTTPAPI(db *oneAndOnlyNumber) {
	m := mux.NewRouter()

	m.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		val, _ := db.getVal()
		fmt.Fprintf(w, "%v", val)
	})

	m.HandleFunc("/set/{newVal}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		newVal, err := strconv.Atoi(vars["newVal"])
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "%v", err)
			return
		}
		db.setVal(newVal)
		fmt.Fprintf(w, "%v", newVal)
	})

	m.HandleFunc("/notify/{curVal}/{curGeneration}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		curVal, err := strconv.Atoi(vars["curVal"])
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "%v", err)
			return
		}
		curGeneration, err := strconv.Atoi(vars["curGeneration"])
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "%v", err)
			return
		}

		if changed := db.notifyValue(curVal, curGeneration); changed {
			log.Printf("notify: val: %v, gen: %v, notifier: %v", curVal, curGeneration, r.URL.Query().Get("notifier"))
		}
		w.WriteHeader(http.StatusOK)
	})
	log.Fatal(http.ListenAndServe(":8080", m))
}
