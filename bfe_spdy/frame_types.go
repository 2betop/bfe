// Copyright (c) 2019 Baidu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bfe_spdy

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"sync"
)

import (
	http "github.com/baidu/bfe/bfe_http"
)

// Version is the protocol version number that this package implements (spdy3.1/spdy3).
const Version = 3

// ControlFrameType stores the type field in a control frame header.
type ControlFrameType uint16

const (
	TypeSynStream    ControlFrameType = 0x0001
	TypeSynReply                      = 0x0002
	TypeRstStream                     = 0x0003
	TypeSettings                      = 0x0004
	TypePing                          = 0x0006
	TypeGoAway                        = 0x0007
	TypeHeaders                       = 0x0008
	TypeWindowUpdate                  = 0x0009
)

// ControlFlags are the flags that can be set on a control frame.
type ControlFlags uint8

const (
	ControlFlagFin                   ControlFlags = 0x01
	ControlFlagUnidirectional                     = 0x02
	ControlFlagSettingsClearSettings              = 0x01
)

// DataFlags are the flags that can be set on a data frame.
type DataFlags uint8

const (
	DataFlagFin DataFlags = 0x01
)

// MaxDataLength is the maximum number of bytes that can be stored in one frame.
const MaxDataLength = 1<<24 - 1

const MinMaxFrameSize = 1 << 14
const MaxFrameSize = 1<<24 - 1

const MaxNumSettings = 1024
const MaxNumHeaders = 1024

// headerValueSepator separates multiple header values.
const headerValueSeparator = "\x00"

// Frame is a single SPDY frame in its unpacked in-memory representation. Use
// Framer to read and write it.
type Frame interface {
	write(f *Framer) error
}

// ControlFrameHeader contains all the fields in a control frame header,
// in its unpacked in-memory representation.
type ControlFrameHeader struct {
	// Note, high bit is the "Control" bit.
	version   uint16 // spdy version number
	frameType ControlFrameType
	Flags     ControlFlags
	length    uint32 // length of data field
}

type controlFrame interface {
	Frame
	read(h ControlFrameHeader, f *Framer) error
}

// StreamId represents a 31-bit value identifying the stream.
type StreamId uint32

// SynStreamFrame is the unpacked, in-memory representation of a SYN_STREAM
// frame.
type SynStreamFrame struct {
	CFHeader             ControlFrameHeader
	StreamId             StreamId
	AssociatedToStreamId StreamId // stream id for a stream which this stream is associated to
	Priority             uint8    // priority of this frame (3-bit)
	Slot                 uint8    // index in the server's credential vector of the client certificate
	Headers              http.Header
}

func (f *SynStreamFrame) StreamEnded() bool {
	return (f.CFHeader.Flags & ControlFlagFin) != 0
}

// SynReplyFrame is the unpacked, in-memory representation of a SYN_REPLY frame.
type SynReplyFrame struct {
	CFHeader ControlFrameHeader
	StreamId StreamId
	Headers  http.Header
}

func (f *SynReplyFrame) StreamEnded() bool {
	return (f.CFHeader.Flags & ControlFlagFin) != 0
}

// RstStreamStatus represents the status that led to a RST_STREAM.
type RstStreamStatus uint32

const (
	ProtocolError RstStreamStatus = iota + 1
	InvalidStream
	RefusedStream
	UnsupportedVersion
	Cancel
	InternalError
	FlowControlError
	StreamInUse
	StreamAlreadyClosed
	InvalidCredentials
	FrameTooLarge
)

// RstStreamFrame is the unpacked, in-memory representation of a RST_STREAM
// frame.
type RstStreamFrame struct {
	CFHeader ControlFrameHeader
	StreamId StreamId
	Status   RstStreamStatus
}

// SettingsFlag represents a flag in a SETTINGS frame.
type SettingsFlag uint8

const (
	FlagSettingsPersistValue SettingsFlag = 0x1
	FlagSettingsPersisted                 = 0x2
)

// SettingsFlag represents the id of an id/value pair in a SETTINGS frame.
type SettingsId uint32

const (
	SettingsUploadBandwidth SettingsId = iota + 1
	SettingsDownloadBandwidth
	SettingsRoundTripTime
	SettingsMaxConcurrentStreams
	SettingsCurrentCwnd
	SettingsDownloadRetransRate
	SettingsInitialWindowSize
	SettingsClientCretificateVectorSize
)

// SettingsFlagIdValue is the unpacked, in-memory representation of the
// combined flag/id/value for a setting in a SETTINGS frame.
type SettingsFlagIdValue struct {
	Flag  SettingsFlag
	Id    SettingsId
	Value uint32
}

// SettingsFrame is the unpacked, in-memory representation of a SPDY
// SETTINGS frame.
type SettingsFrame struct {
	CFHeader     ControlFrameHeader
	FlagIdValues []SettingsFlagIdValue
}

// PingFrame is the unpacked, in-memory representation of a PING frame.
type PingFrame struct {
	CFHeader ControlFrameHeader
	Id       uint32 // unique id for this ping, from server is even, from client is odd.
}

// GoAwayStatus represents the status in a GoAwayFrame.
type GoAwayStatus uint32

const (
	GoAwayOK GoAwayStatus = iota
	GoAwayProtocolError
	GoAwayInternalError
)

// GoAwayFrame is the unpacked, in-memory representation of a GOAWAY frame.
type GoAwayFrame struct {
	CFHeader         ControlFrameHeader
	LastGoodStreamId StreamId // last stream id which was accepted by sender
	Status           GoAwayStatus
}

// HeadersFrame is the unpacked, in-memory representation of a HEADERS frame.
type HeadersFrame struct {
	CFHeader ControlFrameHeader
	StreamId StreamId
	Headers  http.Header
}

// WindowUpdateFrame is the unpacked, in-memory representation of a
// WINDOW_UPDATE frame.
type WindowUpdateFrame struct {
	CFHeader        ControlFrameHeader
	StreamId        StreamId
	DeltaWindowSize uint32 // additional number of bytes to existing window size
}

// TODO: Implement credential frame and related methods.

// DataFrame is the unpacked, in-memory representation of a DATA frame.
type DataFrame struct {
	// Note, high bit is the "Control" bit. Should be 0 for data frames.
	StreamId StreamId
	Flags    DataFlags
	Data     []byte // payload data of this frame
}

func (f *DataFrame) StreamEnded() bool {
	return (f.Flags & DataFlagFin) != 0
}

func (f *DataFrame) String() string {
	return fmt.Sprintf("DataFrame(StreamId:%d, Flags:%d, Data:%d)",
		f.StreamId, f.Flags, len(f.Data))
}

// Special frame for flush connection buffer
type FlushFrame struct{}

// Special frame for finish connection
type FinFrame struct{}

// Special frame for reset stream with panic
type PanicFrame struct{}

// A SPDY specific error.
type ErrorCode string

const (
	UnlowercasedHeaderName     ErrorCode = "header was not lowercased"
	DuplicateHeaders                     = "multiple headers with same name"
	WrongCompressedPayloadSize           = "compressed payload size was incorrect"
	UnknownFrameType                     = "unknown frame type"
	InvalidControlFrame                  = "invalid control frame"
	InvalidDataFrame                     = "invalid data frame"
	InvalidHeaderPresent                 = "frame contained invalid header"
	ZeroStreamId                         = "stream id zero is disallowed"
	ErrTooLongUrl                        = "url is too long"
)

// Error contains both the type of error and additional values. StreamId is 0
// if Error is not associated with a stream.
type Error struct {
	Err      ErrorCode
	StreamId StreamId
}

func (e *Error) Error() string {
	return string(e.Err)
}

var invalidReqHeaders = map[string]bool{
	"Connection":        true,
	"Host":              true,
	"Keep-Alive":        true,
	"Proxy-Connection":  true,
	"Transfer-Encoding": true,
}

var invalidRespHeaders = map[string]bool{
	"Connection":        true,
	"Keep-Alive":        true,
	"Proxy-Connection":  true,
	"Transfer-Encoding": true,
}

// Framer handles serializing/deserializing SPDY frames, including compressing/
// decompressing payloads.
type Framer struct {
	// for unit test and debug usage. Header block is always compressed
	// using zlib compression in SPDY, See 2.6.10.1.
	headerCompressionDisabled bool

	w                  io.Writer
	headerBuf          *bytes.Buffer
	headerCompressor   *zlib.Writer
	r                  io.Reader
	headerReader       io.LimitedReader
	headerDecompressor io.ReadCloser
	maxReadFrameSize   int

	// max uri size
	MaxHeaderUriSize uint32
}

var zlibWriterPool = sync.Pool{
	New: func() interface{} {
		compressor, err := zlib.NewWriterLevelDict(nil, zlib.BestCompression, []byte(headerDictionary))
		if err != nil {
			return nil
		}
		return compressor
	},
}

// NewFramer allocates a new Framer for a given SPDY connection, represented by
// a io.Writer and io.Reader. Note that Framer will read and write individual fields
// from/to the Reader and Writer, so the caller should pass in an appropriately
// buffered implementation to optimize performance.
func NewFramer(w io.Writer, r io.Reader) (*Framer, error) {
	compressBuf := new(bytes.Buffer)
	writer := zlibWriterPool.Get()
	if writer == nil {
		return nil, fmt.Errorf("error create zlib writer")
	}
	compressor := writer.(*zlib.Writer)
	compressor.Reset(compressBuf)

	framer := &Framer{
		w:                w,
		headerBuf:        compressBuf,
		headerCompressor: compressor,
		r:                r,
	}
	return framer, nil
}

func (f *Framer) ReleaseWriter() {
	compressor := f.headerCompressor
	f.headerCompressor = nil
	zlibWriterPool.Put(compressor)
}
