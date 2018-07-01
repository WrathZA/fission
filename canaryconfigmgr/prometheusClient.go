package canaryconfigmgr

import (
	"fmt"
	"time"
	"golang.org/x/net/context"

	promApi1 "github.com/prometheus/client_golang/api/prometheus"
	"github.com/prometheus/common/model"
	log "github.com/sirupsen/logrus"
	"math"
)

type PrometheusApiClient struct {
	client promApi1.QueryAPI
}

// TODO  prometheusSvc will need to come from helm chart value and passed to controller pod.
// controllerpod then passes this during canaryConfigMgr create
func MakePrometheusClient(prometheusSvc string) *PrometheusApiClient {
	log.Printf("Making prom client with service : %s", prometheusSvc)
	promApiConfig := promApi1.Config{
		Address: prometheusSvc,
	}

	promApiClient, err := promApi1.New(promApiConfig)
	if err != nil {
		log.Errorf("Error creating prometheus api client for svc : %s, err : %v", prometheusSvc, err)
	}

	apiQueryClient := promApi1.NewQueryAPI(promApiClient)

	log.Printf("Successfully made prom client")
	return &PrometheusApiClient{
		client: apiQueryClient,
	}
}

// TODO : Add retries in cases of dial tcp error
func(promApi *PrometheusApiClient) GetTotalRequestToUrl(path string, method string, window time.Duration) (float64, error) {
	queryString := fmt.Sprintf("fission_function_calls_total{path=\"%s\",method=\"%s\"}", path, method)
	log.Printf("Querying total function calls for : %s in time window : %v", queryString, window)
	rangeTime  := promApi1.Range{
		End: time.Now(),
		Start: time.Now().Add(-window),
		Step: window,
	}
	val, err := promApi.client.QueryRange(context.Background(), queryString, rangeTime)
	if err != nil {
		log.Errorf("Error querying prometheus for queryrange, qs : %v, rangeTime : %v, err : %v", queryString, rangeTime, err)
		return 0, err
	}

	log.Printf("Value retrieved from query : %v", val)

	totalRequestToUrl := extractValueFromQueryResult(val)
	log.Printf("total calls to this url %v method %v : %v", path, method, totalRequestToUrl)

	return totalRequestToUrl, nil
}

// TODO : Add retries in cases of dial tcp error
func (promApi *PrometheusApiClient) GetTotalFailedRequestsToFunc(funcName string, funcNs string, window time.Duration) (float64, error) {
	queryString := fmt.Sprintf("fission_function_errors_total{name=\"%s\",namespace=\"%s\"}", funcName, funcNs)
	log.Printf("Querying fission_function_errors_total qs : %s in time window : %v", queryString, window)
	rangeTime  := promApi1.Range{
		End: time.Now(),
		Start: time.Now().Add(-window),
		Step: window,
	}
	val, err := promApi.client.QueryRange(context.Background(), queryString, rangeTime)
	if err != nil {
		log.Errorf("Error querying prometheus for queryrange, qs : %v, rangeTime : %v, err : %v", queryString, rangeTime, err)
		return 0, err
	}

	log.Printf("Value retrieved from query : %v", val)

	totalFailedRequestToFunc := extractValueFromQueryResult(val)
	log.Printf("total failed calls to function: %v.%v : %v", funcName, funcNs, window)

	return totalFailedRequestToFunc, nil
}

func(promApi *PrometheusApiClient) GetFunctionFailurePercentage(path, method, funcName, funcNs string, window time.Duration) (float64, error) {

	// first get a total count of requests to this url in a time window
	totalRequestToUrl, err := promApi.GetTotalRequestToUrl(path, method, window)
	if err != nil {
		return 0, err
	}

	if totalRequestToUrl == 0 {
		return -1, fmt.Errorf("no requests to this url %v and method %v in the window : %v", path, method, window)
	}

	// next, get a total count of errored out requests to this function in the same window
	totalFailedRequestToFunc, err := promApi.GetTotalFailedRequestsToFunc(funcName, funcNs, window)
	if err != nil {
		return 0, err
	}

	// calculate the failure percentage of the function
	failurePercentForFunc := (totalFailedRequestToFunc / totalRequestToUrl) * 100
	log.Printf("Final failurePercentForFunc for func: %v.%v is %v", funcName, funcNs, failurePercentForFunc)

	return failurePercentForFunc, nil
}

func extractValueFromQueryResult(val model.Value) float64 {
	switch {
	case val.Type() == model.ValScalar:
		log.Printf("Value type is scalar")
		scalarVal := val.(*model.Scalar)
		log.Printf("scalarValue : %v", scalarVal.Value)
		return float64(scalarVal.Value)

		// handle scalar stuff
	case val.Type() == model.ValVector:
		log.Printf("value type is vector")
		vectorVal := val.(model.Vector)
		total := float64(0)
		for _, elem := range vectorVal {
			log.Printf("labels : %v, Elem value : %v, timestamp : %v", elem.Metric, elem.Value, elem.Timestamp)
			total = total + float64(elem.Value)
		}
		return total

	case val.Type() == model.ValMatrix:
		log.Printf("value type is matrix")
		matrixVal := val.(model.Matrix)
		total := float64(0)
		for _, elem := range matrixVal {
			if len(elem.Values) > 1 {
				firstValue := float64(elem.Values[0].Value)
				lastValue := float64(elem.Values[len(elem.Values)-1].Value)
				log.Printf("labels : %v, firstValue: %v @ ts : %v, lastValue : %v @ts : %v ", elem.Metric, firstValue, elem.Values[0].Timestamp, lastValue, elem.Values[len(elem.Values)-1].Timestamp)

				diff := math.Abs(lastValue - firstValue)
				log.Printf("diff : %v", diff)
				total += diff
			} else {
				log.Printf("Only one value, so taking the 0th elem")
				total += float64(elem.Values[0].Value)
			}
		}
		log.Printf("Final total : %v", total)
		return total

	default:
		log.Printf("type unrecognized")
		return 0
	}
}