package redistest

import (
	"fmt"
	"sort"
)

type Record struct{ Key, Dump string }

func Snapshot(client *Client) ([]Record, error) {
	cursor := "0"
	var keys []string
	for {
		reply, err := client.Do("SCAN", cursor)
		if err != nil {
			return nil, err
		}
		if len(reply.Elements) != 2 {
			return nil, fmt.Errorf("invalid SCAN response")
		}
		cursor = reply.Elements[0].Str
		for _, key := range reply.Elements[1].Elements {
			keys = append(keys, key.Str)
		}
		if cursor == "0" {
			break
		}
	}
	sort.Strings(keys)
	records := make([]Record, 0, len(keys))
	last := ""
	hasLast := false
	for _, key := range keys {
		if hasLast && key == last {
			continue
		}
		reply, err := client.Do("DUMP", key)
		if err != nil {
			return nil, err
		}
		records = append(records, Record{key, reply.Str})
		last, hasLast = key, true
	}
	return records, nil
}
