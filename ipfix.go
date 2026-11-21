package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"sync"

	"github.com/netsampler/goflow2/v2/utils"
)

type ipfixSet struct {
	id   uint16
	data []byte
}

var missingIPFIXTemplates sync.Map

func decodeNetFlowMessage(pipe *utils.NetFlowPipe, message *utils.Message) error {
	if len(message.Payload) < 2 || binary.BigEndian.Uint16(message.Payload[:2]) != 10 {
		return pipe.DecodeFlow(message)
	}

	header, sets, observationDomain, err := splitIPFIXMessage(message.Payload)
	if err != nil {
		return pipe.DecodeFlow(message)
	}

	var templateSets []ipfixSet
	for _, set := range sets {
		if set.id == 2 || set.id == 3 {
			templateSets = append(templateSets, set)
		}
	}
	if len(templateSets) > 0 {
		templateMessage := *message
		templateMessage.Payload = buildIPFIXMessage(header, templateSets)
		if err := pipe.DecodeFlow(&templateMessage); err != nil {
			return err
		}
	}

	templatesBySource := pipe.GetTemplatesForAllSources()[message.Src.String()]
	decodableSets, missingIDs := selectIPFIXDataSets(sets, func(templateID uint16) bool {
		key := uint64(10)<<48 | uint64(observationDomain)<<16 | uint64(templateID)
		_, ok := templatesBySource[key]
		return ok
	})
	for _, templateID := range missingIDs {
		logMissingIPFIXTemplate(message, observationDomain, templateID)
	}
	for _, set := range decodableSets {
		missingIPFIXTemplates.Delete(ipfixTemplateLogKey(message, observationDomain, set.id))
	}

	if len(decodableSets) == 0 {
		return nil
	}
	dataMessage := *message
	dataMessage.Payload = buildIPFIXMessage(header, decodableSets)
	return pipe.DecodeFlow(&dataMessage)
}

func selectIPFIXDataSets(sets []ipfixSet, hasTemplate func(uint16) bool) ([]ipfixSet, []uint16) {
	var decodable []ipfixSet
	missing := make(map[uint16]struct{})
	for _, set := range sets {
		if set.id < 256 {
			continue
		}
		if hasTemplate(set.id) {
			decodable = append(decodable, set)
			continue
		}
		missing[set.id] = struct{}{}
	}

	missingIDs := make([]uint16, 0, len(missing))
	for id := range missing {
		missingIDs = append(missingIDs, id)
	}
	return decodable, missingIDs
}

func splitIPFIXMessage(payload []byte) ([]byte, []ipfixSet, uint32, error) {
	if len(payload) < 16 || binary.BigEndian.Uint16(payload[:2]) != 10 {
		return nil, nil, 0, fmt.Errorf("invalid IPFIX header")
	}

	length := int(binary.BigEndian.Uint16(payload[2:4]))
	if length < 16 || length > len(payload) {
		return nil, nil, 0, fmt.Errorf("invalid IPFIX message length %d", length)
	}
	header := append([]byte(nil), payload[:16]...)
	observationDomain := binary.BigEndian.Uint32(payload[12:16])

	var sets []ipfixSet
	for offset := 16; offset < length; {
		if length-offset < 4 {
			return nil, nil, 0, fmt.Errorf("truncated IPFIX set header")
		}
		setID := binary.BigEndian.Uint16(payload[offset : offset+2])
		setLength := int(binary.BigEndian.Uint16(payload[offset+2 : offset+4]))
		if setLength < 4 || setLength > length-offset {
			return nil, nil, 0, fmt.Errorf("invalid IPFIX set length %d", setLength)
		}
		sets = append(sets, ipfixSet{
			id:   setID,
			data: append([]byte(nil), payload[offset:offset+setLength]...),
		})
		offset += setLength
	}
	return header, sets, observationDomain, nil
}

func buildIPFIXMessage(header []byte, sets []ipfixSet) []byte {
	payload := append([]byte(nil), header...)
	for _, set := range sets {
		payload = append(payload, set.data...)
	}
	binary.BigEndian.PutUint16(payload[2:4], uint16(len(payload)))
	return payload
}

func ipfixTemplateLogKey(message *utils.Message, observationDomain uint32, templateID uint16) string {
	return fmt.Sprintf("%s/%d/%d", message.Src, observationDomain, templateID)
}

func logMissingIPFIXTemplate(message *utils.Message, observationDomain uint32, templateID uint16) {
	key := ipfixTemplateLogKey(message, observationDomain, templateID)
	if _, loaded := missingIPFIXTemplates.LoadOrStore(key, struct{}{}); !loaded {
		log.Printf(
			"IPFIX data set %d from %s awaiting template (observation domain %d); skipping until template arrives",
			templateID, message.Src, observationDomain,
		)
	}
}
