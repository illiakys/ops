package gcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nanovms/ops/lepton"
	"github.com/nanovms/ops/types"
	"github.com/olekukonko/tablewriter"
	compute "google.golang.org/api/compute/v1"
)

// CreateInstance - Creates instance on Google Cloud Platform
func (p *GCloud) CreateInstance(ctx *lepton.Context) error {
	c := ctx.Config()
	if c.CloudConfig.Flavor == "" {
		c.CloudConfig.Flavor = "g1-small"
	}

	if c.RunConfig.InstanceGroup != "" {
		p.addToInstanceGroup(ctx, c.RunConfig.InstanceGroup)
		return nil
	}

	nic, err := p.getNIC(ctx, p.Service)
	if err != nil {
		return err
	}

	machineType := fmt.Sprintf("zones/%s/machineTypes/%s", c.CloudConfig.Zone, c.CloudConfig.Flavor)

	imageName := fmt.Sprintf("projects/%v/global/images/%v",
		c.CloudConfig.ProjectID,
		c.CloudConfig.ImageName)

	c.CloudConfig.Tags = append(c.CloudConfig.Tags, types.Tag{
		Key:   "image",
		Value: c.CloudConfig.ImageName,
	})

	serialTrue := "true"

	labels := map[string]string{}
	for _, tag := range ctx.Config().CloudConfig.Tags {
		labels[tag.Key] = tag.Value
	}

	instanceName := c.RunConfig.InstanceName

	rb := &compute.Instance{
		Name:        instanceName,
		MachineType: machineType,
		Disks: []*compute.AttachedDisk{
			{
				AutoDelete: true,
				Boot:       true,
				Type:       "PERSISTENT",
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: imageName,
				},
			},
		},
		NetworkInterfaces: nic,
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   "serial-port-enable",
					Value: &serialTrue,
				},
			},
		},
		Labels: buildGcpTags(ctx.Config().CloudConfig.Tags),
		Tags: &compute.Tags{
			Items: []string{instanceName},
		},
	}
	op, err := p.Service.Instances.Insert(c.CloudConfig.ProjectID, c.CloudConfig.Zone, rb).Context(context.TODO()).Do()
	if err != nil {
		return err
	}
	fmt.Printf("Instance creation started using image %s. Monitoring operation %s.\n", imageName, op.Name)
	err = p.pollOperation(context.TODO(), c.CloudConfig.ProjectID, p.Service, *op)
	if err != nil {
		return err
	}
	fmt.Printf("Instance creation succeeded %s.\n", instanceName)

	// create dns zones/records to associate DNS record to instance IP
	if c.CloudConfig.DomainName != "" {
		instance, err := p.Service.Instances.Get(c.CloudConfig.ProjectID, c.CloudConfig.Zone, instanceName).Do()
		if err != nil {
			ctx.Logger().Errorf("failed getting instance")
			return err
		}

		cinstance := p.convertToCloudInstance(instance)

		if len(cinstance.PublicIps) != 0 {
			ctx.Logger().Infof("Assigning IP %s to %s", cinstance.PublicIps[0], c.CloudConfig.DomainName)
			err := lepton.CreateDNSRecord(ctx.Config(), cinstance.PublicIps[0], p)
			if err != nil {
				return err
			}
		}
	}

	// create firewall rules to expose instance ports
	if len(ctx.Config().RunConfig.Ports) != 0 {

		if ctx.Config().CloudConfig.EnableIPv6 {
			rule := p.buildFirewallRule("tcp", ctx.Config().RunConfig.Ports, instanceName, ctx.Config().CloudConfig.Subnet, true)

			_, err = p.Service.Firewalls.Insert(c.CloudConfig.ProjectID, rule).Context(context.TODO()).Do()

			if err != nil {
				fmt.Println(err)

				ctx.Logger().Errorf("%v", err)
				return errors.New("Failed to add Firewall rule")
			}

		}

		rule := p.buildFirewallRule("tcp", ctx.Config().RunConfig.Ports, instanceName, ctx.Config().CloudConfig.Subnet, false)

		_, err = p.Service.Firewalls.Insert(c.CloudConfig.ProjectID, rule).Context(context.TODO()).Do()

		if err != nil {
			ctx.Logger().Errorf("%v", err)
			return errors.New("Failed to add Firewall rule")
		}
	}

	if len(ctx.Config().RunConfig.UDPPorts) != 0 {

		if ctx.Config().CloudConfig.EnableIPv6 {
			rule := p.buildFirewallRule("udp", ctx.Config().RunConfig.UDPPorts, instanceName, ctx.Config().CloudConfig.Subnet, true)

			_, err = p.Service.Firewalls.Insert(c.CloudConfig.ProjectID, rule).Context(context.TODO()).Do()

			if err != nil {
				fmt.Println(err)

				ctx.Logger().Errorf("%v", err)
				return errors.New("Failed to add Firewall rule")
			}

		}

		rule := p.buildFirewallRule("udp", ctx.Config().RunConfig.UDPPorts, instanceName, ctx.Config().CloudConfig.Subnet, false)

		_, err = p.Service.Firewalls.Insert(c.CloudConfig.ProjectID, rule).Context(context.TODO()).Do()

		if err != nil {
			ctx.Logger().Errorf("%v", err)
			return errors.New("Failed to add Firewall rule")
		}
	}

	return nil
}

// ListInstances lists instances on Gcloud
func (p *GCloud) ListInstances(ctx *lepton.Context) error {
	instances, err := p.GetInstances(ctx)
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Name", "Status", "Created", "Private Ips", "Public Ips", "Image"})
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor})
	table.SetRowLine(true)
	for _, instance := range instances {
		var rows []string
		rows = append(rows, instance.Name)
		rows = append(rows, instance.Status)
		rows = append(rows, instance.Created)
		rows = append(rows, strings.Join(instance.PrivateIps, ","))
		rows = append(rows, strings.Join(instance.PublicIps, ","))
		rows = append(rows, instance.Image)
		table.Append(rows)
	}
	table.Render()
	return nil
}

// GetInstanceByName returns instance with given name
func (p *GCloud) GetInstanceByName(ctx *lepton.Context, name string) (*lepton.CloudInstance, error) {
	req := p.Service.Instances.Get(ctx.Config().CloudConfig.ProjectID, ctx.Config().CloudConfig.Zone, name)

	instance, err := req.Do()
	if err != nil {
		return nil, err
	}

	if instance == nil {
		return nil, lepton.ErrInstanceNotFound(name)
	}

	return p.convertToCloudInstance(instance), nil
}

// GetInstances return all instances on GCloud
func (p *GCloud) GetInstances(ctx *lepton.Context) ([]lepton.CloudInstance, error) {
	context := context.TODO()
	var (
		cinstances []lepton.CloudInstance
		req        = p.Service.Instances.List(ctx.Config().CloudConfig.ProjectID, ctx.Config().CloudConfig.Zone)
	)

	if err := req.Pages(context, func(page *compute.InstanceList) error {
		for _, instance := range page.Items {
			if val, ok := instance.Labels["createdby"]; ok && val == "ops" {
				cinstance := p.convertToCloudInstance(instance)

				if instance.Labels["image"] != "" {
					cinstance.Image = instance.Labels["image"]
				}

				cinstances = append(cinstances, *cinstance)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return cinstances, nil
}

func (p *GCloud) convertToCloudInstance(instance *compute.Instance) *lepton.CloudInstance {
	var (
		privateIps, publicIps []string
	)
	for _, ninterface := range instance.NetworkInterfaces {
		if ninterface.NetworkIP != "" {
			privateIps = append(privateIps, ninterface.NetworkIP)

		}
		for _, accessConfig := range ninterface.AccessConfigs {
			if accessConfig.NatIP != "" {
				publicIps = append(publicIps, accessConfig.NatIP)
			}
		}
	}

	return &lepton.CloudInstance{
		Name:       instance.Name,
		Status:     instance.Status,
		Created:    instance.CreationTimestamp,
		PublicIps:  publicIps,
		PrivateIps: privateIps,
	}
}

// DeleteInstance deletes instance from Gcloud
func (p *GCloud) DeleteInstance(ctx *lepton.Context, instancename string) error {
	context := context.TODO()
	cloudConfig := ctx.Config().CloudConfig
	runConfig := ctx.Config().RunConfig
	if len(runConfig.Ports) != 0 {

		if cloudConfig.EnableIPv6 {
			rule := p.buildFirewallRule("tcp", runConfig.Ports, instancename, ctx.Config().CloudConfig.Subnet, true)
			_, err := p.Service.Firewalls.Delete(cloudConfig.ProjectID, rule.Name).Context(context).Do()
			if err != nil {
				ctx.Logger().Errorf("%v", err)
				return errors.New("Failed to delete firewall rule")
			}
		}

		rule := p.buildFirewallRule("tcp", runConfig.Ports, instancename, ctx.Config().CloudConfig.Subnet, false)
		_, err := p.Service.Firewalls.Delete(cloudConfig.ProjectID, rule.Name).Context(context).Do()
		if err != nil {
			ctx.Logger().Errorf("%v", err)
			return errors.New("Failed to delete firewall rule")
		}
	}

	if len(runConfig.UDPPorts) != 0 {
		if cloudConfig.EnableIPv6 {
			rule := p.buildFirewallRule("udp", runConfig.UDPPorts, instancename, ctx.Config().CloudConfig.Subnet, true)
			_, err := p.Service.Firewalls.Delete(cloudConfig.ProjectID, rule.Name).Context(context).Do()
			if err != nil {
				ctx.Logger().Errorf("%v", err)
				return errors.New("Failed to delete firewall rule")
			}
		}

		rule := p.buildFirewallRule("udp", runConfig.UDPPorts, instancename, ctx.Config().CloudConfig.Subnet, false)
		_, err := p.Service.Firewalls.Delete(cloudConfig.ProjectID, rule.Name).Context(context).Do()
		if err != nil {
			ctx.Logger().Errorf("%v", err)
			return errors.New("Failed to delete firewall rule")
		}
	}

	op, err := p.Service.Instances.Delete(cloudConfig.ProjectID, cloudConfig.Zone, instancename).Context(context).Do()
	if err != nil {
		return err
	}

	fmt.Printf("Instance deletion started. Monitoring operation %s.\n", op.Name)
	err = p.pollOperation(context, cloudConfig.ProjectID, p.Service, *op)
	if err != nil {
		return err
	}

	if cloudConfig.DomainName != "" {
		domainName := cloudConfig.DomainName
		domainParts := strings.Split(domainName, ".")
		zoneName := domainParts[len(domainParts)-2]
		dnsName := zoneName + "." + domainParts[len(domainParts)-1]
		aRecordName := domainName + "."

		zoneID, err := p.FindOrCreateZoneIDByName(ctx.Config(), dnsName)
		if err != nil {
			return err
		}
		err = p.DeleteZoneRecordIfExists(ctx.Config(), zoneID, aRecordName)
		if err != nil {
			return err
		}

	}

	fmt.Printf("Instance deletion succeeded %s.\n", instancename)
	return nil
}

// StartInstance starts an instance in GCloud
func (p *GCloud) StartInstance(ctx *lepton.Context, instancename string) error {

	context := context.TODO()

	cloudConfig := ctx.Config().CloudConfig
	op, err := p.Service.Instances.Start(cloudConfig.ProjectID, cloudConfig.Zone, instancename).Context(context).Do()
	if err != nil {
		return err
	}

	fmt.Printf("Instance started. Monitoring operation %s.\n", op.Name)
	err = p.pollOperation(context, cloudConfig.ProjectID, p.Service, *op)
	if err != nil {
		return err
	}

	fmt.Printf("Instance started %s.\n", instancename)
	return nil

}

// StopInstance stops instance
func (p *GCloud) StopInstance(ctx *lepton.Context, instancename string) error {
	context := context.TODO()

	cloudConfig := ctx.Config().CloudConfig
	op, err := p.Service.Instances.Stop(cloudConfig.ProjectID, cloudConfig.Zone, instancename).Context(context).Do()
	if err != nil {
		return err
	}

	fmt.Printf("Instance stopping started. Monitoring operation %s.\n", op.Name)
	err = p.pollOperation(context, cloudConfig.ProjectID, p.Service, *op)
	if err != nil {
		return err
	}

	fmt.Printf("Instance stop succeeded %s.\n", instancename)
	return nil
}

// ResetInstance resets instance
func (p *GCloud) ResetInstance(ctx *lepton.Context, instancename string) error {
	context := context.TODO()

	cloudConfig := ctx.Config().CloudConfig
	op, err := p.Service.Instances.Reset(cloudConfig.ProjectID, cloudConfig.Zone, instancename).Context(context).Do()
	if err != nil {
		return err
	}

	fmt.Printf("Instance reseting started. Monitoring operation %s.\n", op.Name)
	err = p.pollOperation(context, cloudConfig.ProjectID, p.Service, *op)
	if err != nil {
		return err
	}

	fmt.Printf("Instance reseting succeeded %s.\n", instancename)
	return nil
}

// PrintInstanceLogs writes instance logs to console
func (p *GCloud) PrintInstanceLogs(ctx *lepton.Context, instancename string, watch bool) error {
	l, err := p.GetInstanceLogs(ctx, instancename)
	if err != nil {
		return err
	}
	fmt.Printf(l)
	return nil
}

// GetInstanceLogs gets instance related logs
func (p *GCloud) GetInstanceLogs(ctx *lepton.Context, instancename string) (string, error) {
	context := context.TODO()

	cloudConfig := ctx.Config().CloudConfig
	lastPos := int64(0)

	resp, err := p.Service.Instances.GetSerialPortOutput(cloudConfig.ProjectID, cloudConfig.Zone, instancename).Start(lastPos).Context(context).Do()
	if err != nil {
		return "", err
	}
	if resp.Contents != "" {
		return resp.Contents, nil
	}

	lastPos = resp.Next
	time.Sleep(time.Second)

	return "", nil
}
