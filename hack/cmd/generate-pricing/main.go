package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/aws/smithy-go"
)

var regionLocation = map[string]string{
	"us-east-1":      "US East (N. Virginia)",
	"us-east-2":      "US East (Ohio)",
	"us-west-2":      "US West (Oregon)",
	"eu-west-1":      "EU (Ireland)",
	"eu-central-1":   "EU (Frankfurt)",
	"ap-southeast-1": "Asia Pacific (Singapore)",
	"ap-southeast-2": "Asia Pacific (Sydney)",
	"ap-northeast-1": "Asia Pacific (Tokyo)",
	"ap-northeast-2": "Asia Pacific (Seoul)",
}

func main() {
	var (
		regionsArg   = flag.String("regions", "us-east-1,us-east-2", "Comma separated AWS regions")
		instanceArg  = flag.String("instance-types", "m5.large,m5.xlarge", "Comma separated EC2 instance types")
		allInstances = flag.Bool("all-instance-types", false, "Discover all EC2 instance types automatically via DescribeInstanceTypes")
		concurrency  = flag.Int("concurrency", 8, "Number of concurrent pricing lookups")
		output       = flag.String("output", "internal/config/aws_prices_gen.go", "Go file to write with the generated map")
	)
	flag.Parse()

	regions := splitAndTrim(*regionsArg)
	var instances []string

	ctx := context.Background()
	// Pricing service is only available in us-east-1 or ap-south-1; default to us-east-1.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	client := pricing.NewFromConfig(cfg)

	if *allInstances {
		instances, err = discoverInstanceTypes(ctx, cfg)
		if err != nil {
			log.Fatalf("discover instance types: %v", err)
		}
		log.Printf("discovered %d instance types", len(instances))
	} else {
		instances = splitAndTrim(*instanceArg)
	}

	if len(regions) == 0 || len(instances) == 0 {
		log.Fatal("regions and instance-types must be provided (directly or via discovery)")
	}

	type job struct {
		region  string
		loc     string
		it      string
		attempt int
	}
	jobs := make([]job, 0)
	for _, region := range regions {
		locationName, ok := regionLocation[region]
		if !ok {
			log.Printf("skipping region %s: location mapping unknown", region)
			continue
		}
		for _, instance := range instances {
			jobs = append(jobs, job{region: region, loc: locationName, it: instance})
		}
	}

	result := make(map[string]map[string]float64)
	concurrencyLevel := *concurrency
	if concurrencyLevel <= 0 {
		concurrencyLevel = 1
	}
	if concurrencyLevel > len(jobs) && len(jobs) > 0 {
		concurrencyLevel = len(jobs)
	}

	var (
		wg    sync.WaitGroup
		tasks = make(chan job, concurrencyLevel*2+1)
		mu    sync.Mutex
		jobWG sync.WaitGroup
	)
	jobWG.Add(len(jobs))

	for i := 0; i < concurrencyLevel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range tasks {
				price, err := fetchOnDemandPrice(ctx, client, job.loc, job.it)
				if err != nil {
					if shouldRetry(err, job.attempt) {
						job.attempt++
						time.Sleep(backoffDelay(job.attempt))
						tasks <- job
						continue
					}
					log.Printf("warning: %v", err)
					jobWG.Done()
					continue
				}
				mu.Lock()
				if _, ok := result[job.region]; !ok {
					result[job.region] = make(map[string]float64)
				}
				result[job.region][job.it] = price
				mu.Unlock()
				jobWG.Done()
			}
		}()
	}

	go func() {
		for _, job := range jobs {
			tasks <- job
		}
	}()

	go func() {
		jobWG.Wait()
		close(tasks)
	}()

	wg.Wait()

	regionsLogged := make([]string, 0, len(result))
	for region := range result {
		regionsLogged = append(regionsLogged, region)
	}
	sort.Strings(regionsLogged)
	for _, region := range regionsLogged {
		instMap := result[region]
		instances := make([]string, 0, len(instMap))
		for inst := range instMap {
			instances = append(instances, inst)
		}
		sort.Strings(instances)
		for _, inst := range instances {
			log.Printf("resolved %s %s => %.5f USD/hr", region, inst, instMap[inst])
		}
	}

	if err := writeGoFile(*output, result); err != nil {
		log.Fatalf("write go file: %v", err)
	}
}

func splitAndTrim(items string) []string {
	raw := strings.Split(items, ",")
	var out []string
	for _, item := range raw {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func fetchOnDemandPrice(ctx context.Context, client *pricing.Client, locationName, instanceType string) (float64, error) {
	input := &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters: []pricingtypes.Filter{
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("instanceType"), Value: aws.String(instanceType)},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("location"), Value: aws.String(locationName)},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("operatingSystem"), Value: aws.String("Linux")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("tenancy"), Value: aws.String("Shared")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("preInstalledSw"), Value: aws.String("NA")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("capacitystatus"), Value: aws.String("Used")},
		},
		MaxResults: aws.Int32(1),
	}
	out, err := client.GetProducts(ctx, input)
	if err != nil {
		return 0, fmt.Errorf("pricing API for %s in %s: %w", instanceType, locationName, err)
	}
	if len(out.PriceList) == 0 {
		return 0, fmt.Errorf("no pricing data for %s in %s", instanceType, locationName)
	}

	var product struct {
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(out.PriceList[0]), &product); err != nil {
		return 0, fmt.Errorf("decode pricing payload for %s in %s: %w", instanceType, locationName, err)
	}

	for _, term := range product.Terms.OnDemand {
		for _, dimension := range term.PriceDimensions {
			if usd, ok := dimension.PricePerUnit["USD"]; ok {
				value, err := strconv.ParseFloat(usd, 64)
				if err != nil {
					return 0, fmt.Errorf("parse price %q for %s in %s: %w", usd, instanceType, locationName, err)
				}
				return value, nil
			}
		}
	}

	return 0, fmt.Errorf("no USD price found for %s in %s", instanceType, locationName)
}

func writeGoFile(path string, data map[string]map[string]float64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.WriteString("// Code generated by hack/cmd/generate-pricing; DO NOT EDIT.\n")
	buf.WriteString("package config\n\n")
	buf.WriteString("// defaultAWSNodePrices returns AWS on-demand prices per region/instance type.\n")
	buf.WriteString("func defaultAWSNodePrices() map[string]map[string]float64 {\n")
	buf.WriteString("\treturn map[string]map[string]float64{\n")

	regions := make([]string, 0, len(data))
	for region := range data {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	for _, region := range regions {
		buf.WriteString(fmt.Sprintf("\t\t%q: {\n", region))
		instanceMap := data[region]
		instances := make([]string, 0, len(instanceMap))
		for instance := range instanceMap {
			instances = append(instances, instance)
		}
		sort.Strings(instances)
		for _, instance := range instances {
			buf.WriteString(fmt.Sprintf("\t\t\t%q: %.6f,\n", instance, instanceMap[instance]))
		}
		buf.WriteString("\t\t},\n")
	}

	buf.WriteString("\t}\n")
	buf.WriteString("}\n")

	return os.WriteFile(path, buf.Bytes(), 0o600)
}

func discoverInstanceTypes(ctx context.Context, cfg aws.Config) ([]string, error) {
	client := ec2.NewFromConfig(cfg)
	paginator := ec2.NewDescribeInstanceTypesPaginator(client, &ec2.DescribeInstanceTypesInput{})
	unique := map[string]struct{}{}

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, it := range page.InstanceTypes {
			unique[string(it.InstanceType)] = struct{}{}
		}
	}

	list := make([]string, 0, len(unique))
	for name := range unique {
		list = append(list, name)
	}
	sort.Strings(list)
	return list, nil
}

const maxPricingAttempts = 5

func shouldRetry(err error, attempt int) bool {
	if attempt >= maxPricingAttempts {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		if apiErr.ErrorCode() == "ThrottlingException" {
			return true
		}
	}
	return false
}

func backoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(attempt*attempt) * 200 * time.Millisecond
	return d
}
