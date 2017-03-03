package main

import (
	"github.com/opennetworkinglab/onos-warden/agent"
	"github.com/opennetworkinglab/onos-warden/warden"
	"time"
	"fmt"
	"sync"
	"reflect"
	"net"
	"encoding/binary"
)

const (
	numCells = 3
	clusterType = "dummy"
)

type client struct {
	grpc  agent.WardenClient
	cells map[string]warden.ClusterAdvertisement
	mux sync.Mutex
}

func NewAgentWorker() (agent.Worker, error) {
	var c client
	c.cells = make(map[string]warden.ClusterAdvertisement)
	return &c, nil
}

func (c *client) Start() {
	for i := 0; i < numCells; i++ {
		c.updateRequest(&warden.ClusterAdvertisement {
			ClusterId: agent.GetWord(string(rune('a' + i))),
			ClusterType: clusterType,
			State: warden.ClusterAdvertisement_AVAILABLE,
			HeadNodeIP: "1.2.3.4",
		})
	}
}

func (c *client) Bind(client agent.WardenClient) {
	c.grpc = client
}

func (c *client) Teardown() {
	c.mux.Lock()
	defer c.mux.Unlock()

	// Don't worry about the map, we are going away
	for _, ad := range c.cells {
		ad.State = warden.ClusterAdvertisement_UNAVAILABLE
		c.grpc.PublishUpdate(&ad)
	}
}

func (c *client) Handle(req *warden.ClusterRequest) {
	if req.ClusterType != clusterType {
		fmt.Println("Cannot handle cluster type", req.ClusterType)
		return
	}
	ad, ok := c.getRequest(req.ClusterId)
	if !ok {
		fmt.Println("Cannot find cluster id", req.ClusterId)
		return
	}
	if ad.RequestId != "" && ad.RequestId != req.RequestId {
		fmt.Printf("Requested id %s does not match exisiting id %s\n", req.RequestId, ad.RequestId)
		return
	}

	switch req.Type {
	case warden.ClusterRequest_RESERVE:
		ad.State = warden.ClusterAdvertisement_RESERVED
		ad.RequestId = req.RequestId
		if req.Spec == nil {
			fmt.Println("req spec is nil", req)
			return
		}
		ad.ReservationInfo = &warden.ClusterAdvertisement_ReservationInfo{
			UserName: req.Spec.UserName,
			Duration: req.Duration,
			ReservationStartTime: uint32(time.Now().Unix()),
		}
		ad.Nodes = make([]*warden.ClusterAdvertisement_ClusterNode, req.Spec.ControllerNodes)
		for i := range ad.Nodes {
			id := uint32(i+1)
			ip := make(net.IP, 4)
			binary.BigEndian.PutUint32(ip, id)
			ad.Nodes[i] = &warden.ClusterAdvertisement_ClusterNode{
				Id: id,
				Ip: ip.String(),
			}
		}
	case warden.ClusterRequest_EXTEND:
		if ad.ReservationInfo == nil {
			fmt.Println("Could not extend reservation; reservation info missing", req)
			return
		}
		// Update the duration field
		start := time.Unix(int64(ad.ReservationInfo.ReservationStartTime), int64(0))
		past := time.Since(start)
		newDuration := int32(float64(req.Duration) + past.Minutes())
		ad.ReservationInfo.Duration = newDuration
	case warden.ClusterRequest_RETURN:
		ad.State = warden.ClusterAdvertisement_AVAILABLE
		ad.RequestId = ""
		ad.Nodes = nil
		ad.ReservationInfo = nil
	}
	c.updateRequest(&ad)
}

func (c *client) getRequest(cId string) (warden.ClusterAdvertisement, bool) {
	c.mux.Lock()
	defer c.mux.Unlock()

	ad, ok := c.cells[cId]
	return ad, ok
}

func (c *client) updateRequest(ad *warden.ClusterAdvertisement) {
	c.mux.Lock()
	defer c.mux.Unlock()

	id := ad.ClusterId
	existing, ok := c.cells[id]
	if !ok || !reflect.DeepEqual(existing, ad) {
		c.cells[id] = *ad
		c.grpc.PublishUpdate(ad)
	}
}

func main() {
	agent.Run(NewAgentWorker())
}
