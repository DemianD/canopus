package canopus

import (
	"log"
	"net"
	"sync"
	"time"
)

func MessageSizeAllowed(req Request) bool {
	msg := req.GetMessage()
	b, _ := MessageToBytes(msg)

	if len(b) > 65536 {
		return false
	}

	return true
}

type UDPConnection struct {
	conn net.Conn
	open bool
}

func (c *UDPConnection) ObserveResource(resource string) (tok string, err error) {
	req := NewRequest(MessageConfirmable, Get)
	req.SetRequestURI(resource)
	req.GetMessage().AddOption(OptionObserve, 0)

	req.GetMessage().AddOption(OptionContentFormat, MediaTypeJSONVndOmaLwm2m)
	req.GetMessage().AddOption(OptionAccept, MediaTypeJSONVndOmaLwm2m)

	req.SetMediaType(MediaTypeJSONVndOmaLwm2m)

	resp, err := c.Send(req)

	if err != nil {
		return "", err
	}

	c.open = true
	tok = string(resp.GetMessage().GetToken())

	return
}

func (c *UDPConnection) CancelObserveResource(resource string, token string) (err error) {
	req := NewRequest(MessageConfirmable, Get)
	req.SetRequestURI(resource)
	req.GetMessage().AddOption(OptionObserve, 1)

	_, err = c.Send(req)
	return
}

func (c *UDPConnection) StopObserve(ch chan ObserveMessage) {
	c.open = false

	close(ch)
}

func (c *UDPConnection) Close() error {
	c.open = false

	return c.conn.Close()
}

func (c *UDPConnection) Observe(ch chan ObserveMessage) {

	readBuf := make([]byte, MaxPacketSize)
	for c.open {
		len, err := c.Read(readBuf, false)
		if err == nil {
			msgBuf := make([]byte, len)
			copy(msgBuf, readBuf)

			msg, err := BytesToMessage(msgBuf)
			if msg.GetOption(OptionObserve) != nil && c.open == true {
				ch <- NewObserveMessage(msg.GetURIPath(), msg.GetPayload(), msg)
			}
			if err != nil {
				log.Println("Error occured reading UDP", err)

			}
		} else {
			log.Println("Error occured reading UDP", err)

		}
	}
}

func (c *UDPConnection) Send(req Request) (resp Response, err error) {
	msg := req.GetMessage()
	opt := msg.GetOption(OptionBlock1)

	if opt == nil { // Block1 was not set
		if MessageSizeAllowed(req) != true {
			return nil, ErrMessageSizeTooLongBlockOptionValNotSet
		}
	} else { // Block1 was set
		// log.Println("Block 1 was set")
	}

	if opt != nil {
		blockOpt := Block1OptionFromOption(opt)
		if blockOpt.Value == nil {
			if MessageSizeAllowed(req) != true {
				err = ErrMessageSizeTooLongBlockOptionValNotSet
				return
			} else {
				// - Block # = one and only block (sz = unspecified), whereas 0 = 16bits
				// - MOre bit = 0
			}
		} else { // BLock transfer request
			payload := msg.GetPayload().GetBytes()
			payloadLen := uint32(len(payload))
			blockSize := blockOpt.BlockSizeLength()
			currSeq := uint32(0)
			totalBlocks := uint32(payloadLen / blockSize)
			completed := false

			var wg sync.WaitGroup
			wg.Add(1)

			for completed == false {
				if currSeq <= totalBlocks {

					var blockPayloadStart uint32
					var blockPayloadEnd uint32
					var blockPayload []byte

					blockPayloadStart = currSeq * uint32(blockSize)

					more := true
					if currSeq == totalBlocks {
						more = false
						blockPayloadEnd = payloadLen
					} else {
						blockPayloadEnd = blockPayloadStart + uint32(blockSize)
					}

					blockPayload = payload[blockPayloadStart : blockPayloadEnd+1]

					blockOpt = NewBlock1Option(blockOpt.Size(), more, currSeq)
					msg.ReplaceOptions(blockOpt.Code, []Option{blockOpt})
					modifiedMsg := msg.(*CoapMessage)
					modifiedMsg.SetMessageId(GenerateMessageID())
					modifiedMsg.SetPayload(NewBytesPayload(blockPayload))

					// send message
					_, err2 := c.SendMessage(msg)
					if err2 != nil {
						wg.Done()
						return
					}
					currSeq = currSeq + 1

				} else {
					completed = true
					wg.Done()
				}
			}
		}
	}
	resp, err = c.SendMessage(msg)
	return
}

func (c *UDPConnection) SendMessage(msg Message) (resp Response, err error) {
	if msg == nil {
		return nil, ErrNilMessage
	}

	b, err := MessageToBytes(msg)
	if err != nil {
		return nil, err
	}

	if msg.GetMessageType() == MessageNonConfirmable {
		go c.Write(b)
		resp = NewResponse(NewEmptyMessage(msg.GetMessageId()), nil)
		return nil, err
	}

	_, err = c.Write(b)
	if err != nil {
		return nil, err
	}

	msgBuf := make([]byte, 1500)
	if msg.GetMessageType() == MessageAcknowledgment {
		resp = NewResponse(NewEmptyMessage(msg.GetMessageId()), nil)
		return
	}

	n, err := c.Read(msgBuf, true)
	if err != nil {
		return nil, err
	}

	respMsg, err := BytesToMessage(msgBuf[:n])
	if err != nil {
		return nil, err
	}
	resp = NewResponse(respMsg, nil)

	if msg.GetMessageType() == MessageConfirmable {
		// TODO: Send out message and wait for a confirm. If confirmation not retrieved,
		// resend (taking into account timeouts and back-off transmissions

		// c.Send(NewRequestFromMessage(msg))
	}
	return
}

func (c *UDPConnection) Write(b []byte) (int, error) {
	return c.conn.Write(b)
}

func (c *UDPConnection) Read(b []byte, timeout bool) (int, error) {

	if timeout == true {
		deadline := time.Now().Add(5 * time.Second)

		c.conn.SetReadDeadline(deadline)
		c.conn.SetWriteDeadline(deadline)
	} else {
		c.conn.SetReadDeadline(time.Time{})
		c.conn.SetWriteDeadline(time.Time{})
	}

	return c.conn.Read(b)
}
