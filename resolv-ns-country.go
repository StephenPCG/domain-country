package main

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/oschwald/geoip2-golang"
	"golang.org/x/net/publicsuffix"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Job func()

type JobPool struct {
	Name string

	sync.Mutex

	concurrent     int
	concurrentPool chan int
	closed         bool
	jobQueue       []Job

	jobAdded    int64
	jobFinished int64
}

func NewJobPool(name string, concurrent int) *JobPool {
	jp := &JobPool{
		Name:           name,
		concurrent:     concurrent,
		concurrentPool: make(chan int, concurrent),
		closed:         false,
		jobAdded:       0,
		jobFinished:    0,
	}
	for i := 0; i < concurrent; i++ {
		jp.concurrentPool <- 1
	}
	return jp
}

// AddJob adds a job to the JobPool, waiting to be scheduled
func (jp *JobPool) AddJob(job Job) {
	if !jp.closed && job != nil {
		jp.Lock()
		jp.jobQueue = append(jp.jobQueue, job)
		jp.Unlock()
		atomic.AddInt64(&jp.jobAdded, 1)
	}
}

func (jp *JobPool) getJob() Job {
	if len(jp.jobQueue) > 0 {
		jp.Lock()
		l := len(jp.jobQueue)
		var job Job
		job, jp.jobQueue = jp.jobQueue[l-1], jp.jobQueue[:l-1]
		jp.Unlock()
		return job
	}
	return nil
}

// Run starts scheduling jobs in the pool
func (jp *JobPool) Run() {
	for {
		if job := jp.getJob(); job != nil {
			<-jp.concurrentPool
			go func() {
				job()
				jp.concurrentPool <- 1
				atomic.AddInt64(&jp.jobFinished, 1)
			}()
		} else {
			if jp.closed {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// Close the job pool, no more jobs can be added
func (jp *JobPool) Close() {
	jp.closed = true
}

// IsFinished tells if all jobs are done
func (jp *JobPool) IsFinished() bool {
	return jp.closed && len(jp.jobQueue) == 0 && len(jp.concurrentPool) == jp.concurrent
}

func (jp *JobPool) PrintStats() {
	fmt.Fprintf(os.Stderr, "%s: added: %d, finished: %d, on going: %d, queue: %d\n",
		jp.Name,
		jp.jobAdded, jp.jobFinished,
		jp.concurrent-len(jp.concurrentPool), len(jp.jobQueue),
	)
}

type Ns struct {
	Name    string // name of the ns
	IPAddr  string // first ip of the ns
	Country string // iso country code of the ip
	Error   error  // error while resolving the ns
}

func (ns *Ns) Resolv() {
	if ips, err := net.LookupIP(ns.Name); err != nil {
		ns.Error = err
	} else {
		ns.IPAddr = ips[0].String()
		if country, err := geoip.Country(ips[0]); err != nil {
			ns.Error = err
		} else {
			ns.Country = country.Country.IsoCode
		}
	}
}

type Domain struct {
	Name   string // name of the domain
	NsName string // first ns of the domain
	Error  error  // error while resolving the domain
}

func (domain *Domain) Resolv() {
	if ns, err := net.LookupNS(domain.Name); err != nil {
		domain.Error = err
	} else {
		if len(ns) == 0 {
			domain.Error = errors.New("len(ns) == 0")
		} else {
			domain.NsName = ns[0].Host
		}
	}
}

var (
	geoip, _      = geoip2.Open("GeoLite2-Country.mmdb")
	domainJobpool = NewJobPool("Domain Resolv", 50)
	nsJobpool     = NewJobPool("NS Resolve", 50)

	domains = make(map[string]*Domain)
	nss     = make(map[string]*Ns)
)

func NewDomainResolvJob(name string) Job {
	if _, ok := domains[name]; !ok {
		domain := &Domain{
			Name: name,
		}
		domains[name] = domain

		return func() {
			if domain.Resolv(); domain.Error == nil {
				nsJobpool.AddJob(NewNsResolvJob(domain.NsName))
			}
		}
	}
	return nil
}

func NewNsResolvJob(name string) Job {
	if _, ok := nss[name]; !ok {
		ns := &Ns{
			Name: name,
		}
		nss[name] = ns

		return func() {
			ns.Resolv()
		}
	}
	return nil
}

func main() {
	go domainJobpool.Run()
	go nsJobpool.Run()

	// get domains from stdin, extract TLDPlusOne and push to domain resolv job pool
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		// skip domains without dot, and just one character
		if len(line) <= 1 || strings.Count(line, ".") == 0 {
			continue
		}
		if domain, err := publicsuffix.EffectiveTLDPlusOne(line); err == nil {
			domainJobpool.AddJob(NewDomainResolvJob(domain))
		}
	}
	domainJobpool.Close()

	// print job pools' status
	go func() {
		for {
			if domainJobpool.IsFinished() && nsJobpool.IsFinished() {
				break
			}
			domainJobpool.PrintStats()
			nsJobpool.PrintStats()
			time.Sleep(1 * time.Second)
		}
	}()

	// wait for domain resolv jobs to finish
	for !domainJobpool.IsFinished() {
		time.Sleep(100 * time.Millisecond)
	}
	nsJobpool.Close()

	// wait for ns resolv jobs to finish
	for !nsJobpool.IsFinished() {
		time.Sleep(100 * time.Millisecond)
	}

	// print job pool stats for the last time
	domainJobpool.PrintStats()
	nsJobpool.PrintStats()

	// dump result
	for name, domain := range domains {
		if domain.Error != nil {
			fmt.Printf("%s|%s\n", name, domain.Error)
			continue
		}
		if ns, ok := nss[domain.NsName]; ok {
			if ns.Error != nil {
				fmt.Printf("%s|%s\n", name, ns.Error)
			} else {
				fmt.Printf("%s|%s|%s|%s\n", name, domain.NsName, ns.IPAddr, ns.Country)
			}
		} else {
			fmt.Printf("%s|NS Not Resolved", domain.NsName)
		}
	}
}
