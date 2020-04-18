package networking

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"
)

var verboseLogging = false

// EnableVerboseLogging enabled verbose logging globally
func EnableVerboseLogging() {
	verboseLogging = true
}

// JSONRequestDynamicHeaders submits an HTTP web request and parses the response into the responseData object as JSON
func JSONRequestDynamicHeaders(
	httpClient *http.Client,
	method string,
	reqURL string,
	data string,
	headers map[string]HeaderFn,
	responseData interface{}, // the passed in responseData should be a pointer
	errorKey string,
) error {
	headersMap := map[string]string{}
	for header, fn := range headers {
		headersMap[header] = fn(method, reqURL, data)
	}

	return JSONRequest(
		httpClient,
		method,
		reqURL,
		data,
		headersMap,
		responseData,
		errorKey,
	)
}

// JSONRequest submits an HTTP web request and parses the response into the responseData object as JSON
func JSONRequest(
	httpClient *http.Client,
	method string,
	reqURL string,
	data string,
	headers map[string]string,
	responseData interface{}, // the passed in responseData should be a pointer
	errorKey string,
) error {
	// create http request
	req, e := http.NewRequest(method, reqURL, strings.NewReader(data))
	if e != nil {
		return fmt.Errorf("could not create http request: %s", e)
	}

	// add headers
	for key, value := range headers {
		req.Header.Add(key, value)
	}

	// execute request
	if verboseLogging {
		log.Printf("executing HTTP request: %s %s %s\n", method, reqURL, data)
	}
	resp, e := httpClient.Do(req)
	if e != nil {
		return fmt.Errorf("could not execute http request: %s", e)
	}
	defer resp.Body.Close()

	// read response
	body, e := ioutil.ReadAll(resp.Body)
	if e != nil {
		return fmt.Errorf("could not read http response: %s", e)
	}
	bodyString := string(body)

	// ensure Content-Type is json
	contentType, _, e := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if e != nil {
		return fmt.Errorf("could not read 'Content-Type' header in http response: %s | response body: %s", e, bodyString)
	}
	if contentType != "application/json" && contentType != "application/hal+json" {
		return fmt.Errorf("invalid 'Content-Type' header in http response ('%s'), expecting 'application/json' or 'application/hal+json', response body: %s", contentType, bodyString)
	}

	if errorKey != "" {
		var errorResponse interface{}
		e = json.Unmarshal(body, &errorResponse)
		if e != nil {
			return fmt.Errorf("could not unmarshall response body to check for an error response: %s | bodyString: %s", e, bodyString)
		}

		switch er := errorResponse.(type) {
		case map[string]interface{}:
			if _, ok := er[errorKey]; ok {
				return fmt.Errorf("error in response, bodyString: %s", bodyString)
			}
		}
	}

	if responseData != nil {
		// parse response, the passed in responseData should be a pointer
		e = json.Unmarshal(body, responseData)
		if e != nil {
			return fmt.Errorf("could not unmarshall response body into json: %s | response body: %s", e, bodyString)
		}
	}

	return nil
}

// DownloadFile downloads a URL to a file on the local disk as it downloads it.
func DownloadFile(url string, filepath string) error {
	outfile, e := os.Create(filepath)
	if e != nil {
		return fmt.Errorf("could not create file at filepath (%s): %s", filepath, e)
	}
	defer outfile.Close()

	resp, e := http.Get(url)
	if e != nil {
		return fmt.Errorf("could not get file at URL (%s): %s", url, e)
	}
	defer resp.Body.Close()

	// do the download and write to file on disk as we download
	_, e = io.Copy(outfile, resp.Body)
	if e != nil {
		return fmt.Errorf("could not download from URL (%s) to file (%s) in a streaming manner: %s", url, filepath, e)
	}
	return nil
}
