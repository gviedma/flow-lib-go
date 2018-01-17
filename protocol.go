package flow

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"reflect"
)

const (
	// protocol headers
	HeaderPrefix       = "FnProject-"
	DatumTypeHeader    = HeaderPrefix + "Datumtype"
	FlowIDHeader       = HeaderPrefix + "FlowID"
	StageIDHeader      = HeaderPrefix + "StageID"
	ResultStatusHeader = HeaderPrefix + "ResultStatus"
	ResultCodeHeader   = HeaderPrefix + "ResultCode"
	CodeLocationHeader = HeaderPrefix + "Codeloc"
	ErrorTypeHeader    = HeaderPrefix + "Errortype"
	MethodHeader       = HeaderPrefix + "Method"

	UserHeaderPrefix = HeaderPrefix + "Header-"

	SuccessHeaderValue = "success"
	FailureHeaderValue = "failure"

	BlobDatumHeader     = "blob"
	EmptyDatumHeader    = "empty"
	ErrorDatumHeader    = "error"
	StageRefDatumHeader = "stageref"
	HTTPReqDatumHeader  = "httpreq"
	HTTPRespDatumHeader = "httpresp"
	StateDatumHeader    = "state"

	// standard headers
	ContentTypeHeader        = "Content-Type"
	JSONMediaHeader          = "application/json"
	GobMediaHeader           = "application/x-gob"
	TextMediaHeader          = "text/plain"
	OctetStreamMediaHeader   = "application/octet-stream"
	DefaultContentTypeHeader = OctetStreamMediaHeader

	MaxContinuationArgCount = 2
)

type completerProtocol struct {
	baseURL string
}

func newCompleterProtocol(baseURL string) *completerProtocol {
	return &completerProtocol{baseURL: baseURL}
}

func (p *completerProtocol) parseFlowID(res *http.Response) flowID {

	return flowID(res.Header.Get(FlowIDHeader))
}

func (p *completerProtocol) parseStageID(res *http.Response) stageID {
	return stageID(res.Header.Get(StageIDHeader))
}

func (p *completerProtocol) newHTTPReq(path string, msg interface{}) *http.Request {
	url := fmt.Sprintf("%s/%s", p.baseURL, path)
	body := new(bytes.Buffer)
	if err := json.NewEncoder(body).Encode(msg); err != nil {
		panic("Failed to encode request object")
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		panic("Failed to create request object")
	}
	return req
}

func (p *completerProtocol) createFlowReq(functionID string) *http.Request {
	url := fmt.Sprintf("%s/flows", p.baseURL)
	msg := CreateGraphRequest{FunctionId: functionID}
	body := new(bytes.Buffer)
	if err := json.NewEncoder(body).Encode(msg); err != nil {
		panic("Failed to encode request object")
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		panic("Failed to create request object")
	}
	return req
}

func (p *completerProtocol) completedValueReq(fid flowID, value interface{}) *http.Request {
	URL := p.rootStageURL("completedValue", fid)
	var req *http.Request
	if err, isErr := value.(error); isErr {
		// errors are encoded as string gobs
		req = createRequest("POST", URL, encodeGob(err.Error()))
		req.Header.Set(ResultStatusHeader, FailureHeaderValue)
	} else {
		req = createRequest("POST", URL, encodeGob(value))
		req.Header.Set(ResultStatusHeader, SuccessHeaderValue)
	}
	req.Header.Set(DatumTypeHeader, BlobDatumHeader)
	req.Header.Set(ContentTypeHeader, GobMediaHeader)
	return req
}

func (p *completerProtocol) rootStageURL(op string, fid flowID) string {
	return fmt.Sprintf("%s/flows/%s/%s", p.baseURL, fid, op)
}

func (p *completerProtocol) chainedStageURL(op string, fid flowID, sid stageID) string {
	return fmt.Sprintf("%s/flows/%s/stage/%s/%s", p.baseURL, fid, sid, op)
}

func (p *completerProtocol) chained(op string, fid flowID, sid stageID, fn interface{}, loc *codeLoc) *http.Request {
	return p.completionWithBody(p.chainedStageURL(op, fid, sid), fn, loc)
}

func (p *completerProtocol) chainedWithOther(op string, fid flowID, sid stageID, altCid stageID, fn interface{}, loc *codeLoc) *http.Request {
	URL := fmt.Sprintf("%s/flows/%s/stage/%s/%s?other=%s", p.baseURL, fid, sid, op, string(altCid))
	return p.completionWithBody(URL, fn, loc)
}

func (p *completerProtocol) completionWithBody(URL string, fn interface{}, loc *codeLoc) *http.Request {
	b, err := json.Marshal(newContinuationRef(fn))
	if err != nil {
		panic("Failed to marshal continuation reference")
	}
	return p.completion(URL, loc, bytes.NewReader(b))
}

func (p *completerProtocol) invokeFunction(URL string, loc *codeLoc, r *HTTPRequest) *http.Request {
	req := createRequest("POST", URL, bytes.NewReader(r.Body))
	req.Header.Set(DatumTypeHeader, HTTPReqDatumHeader)
	req.Header.Set(MethodHeader, r.Method)
	cType := r.Headers.Get(ContentTypeHeader)
	if cType == "" {
		cType = DefaultContentTypeHeader
	}
	req.Header.Set(ContentTypeHeader, cType)
	req.Header.Set(CodeLocationHeader, loc.String())
	for k, v := range r.Headers {
		// don't allow duplicate values for the same key
		req.Header.Set(UserHeaderPrefix+k, v[0])
	}
	return req
}

func (p *completerProtocol) completion(URL string, loc *codeLoc, r io.Reader) *http.Request {
	req := createRequest("POST", URL, r)
	req.Header.Set(ContentTypeHeader, JSONMediaHeader)
	req.Header.Set(DatumTypeHeader, BlobDatumHeader)
	req.Header.Set(CodeLocationHeader, loc.String())
	return req
}

func (p *completerProtocol) getStageReq(fid flowID, sid stageID) *http.Request {
	return createRequest("GET", fmt.Sprintf("%s/flows/%s/stage/%s/await", p.baseURL, fid, sid), nil)
}

func (p *completerProtocol) commit(fid flowID) *http.Request {
	return createRequest("POST", fmt.Sprintf("%s/flows/%s/commit", p.baseURL, fid), nil)
}

// panics if the request can't be created
func createRequest(method string, url string, r io.Reader) *http.Request {
	debug(fmt.Sprintf("Requesting URL %s", url))
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		panic("Failed to create request object")
	}
	return req
}

func encodeGob(value interface{}) *bytes.Buffer {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(value); err != nil {
		panic("Failed to encode gob: " + err.Error())
	}
	return &buf
}

func encodeContinuationRef(fn interface{}) *bytes.Buffer {
	cr := newContinuationRef(fn)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(cr); err != nil {
		panic("Failed to encode continuation reference: " + err.Error())
	}
	return &buf
}

func continuationArgTypes(continuation interface{}) (argTypes []reflect.Type) {
	if reflect.TypeOf(continuation).Kind() != reflect.Func {
		panic("Continuation must be a function!")
	}

	fn := reflect.TypeOf(continuation)
	argC := fn.NumIn() // inbound params
	if argC > MaxContinuationArgCount {
		panic(fmt.Sprintf("Continuations may take a maximum of %d parameters", MaxContinuationArgCount))
	}
	argTypes = make([]reflect.Type, argC)
	for i := 0; i < argC; i++ {
		argTypes[i] = fn.In(i)
	}
	return
}

func decodeContinuationArg(continuation interface{}, argIndex int, reader io.Reader, header *textproto.MIMEHeader) interface{} {
	argTypes := continuationArgTypes(continuation)
	if len(argTypes) < argIndex {
		panic("Invalid number of arguments decoded for continuation")
	} else if len(argTypes) == 0 {
		debug("Ignoring datum parameter for no-arg function")
		return nil
	}
	return decodeDatum(argTypes[argIndex], reader, header)
}
