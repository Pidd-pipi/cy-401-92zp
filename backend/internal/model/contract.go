package model

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// ContractStage is one payment milestone of a contract.
//
// Status flow:
//
//	pending -> in_progress -> submitted -> paid
//	                         submitted -> rejected -> submitted
//
// The next stage only moves to in_progress after the previous one is paid.
type ContractStage struct {
	Name         string  `json:"name"`
	Amount       float64 `json:"amount"`
	Status       string  `json:"status"` // pending / in_progress / submitted / rejected / paid
	DueAt        string  `json:"dueAt"`
	RejectReason string  `json:"rejectReason,omitempty"`
	SubmittedAt  string  `json:"submittedAt,omitempty"`
	PaidAt       string  `json:"paidAt,omitempty"`
}

// Contract is the signed agreement between requester and freelancer.
type Contract struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	ContractNo    string    `gorm:"size:64;uniqueIndex;not null" json:"contractNo"`
	TotalAmount   float64   `gorm:"type:decimal(14,2);not null" json:"totalAmount"`
	PaymentType   string    `gorm:"size:16;not null;default:one_time" json:"paymentType"`
	StagesJS      string    `gorm:"column:stages;type:text" json:"-"`
	Status        string    `gorm:"size:24;not null;default:pending_signature" json:"status"`
	RequirementID uint      `gorm:"index;not null" json:"requirementId"`
	PartyAID      uint      `gorm:"index;not null" json:"partyAId"`
	PartyBID      uint      `gorm:"index;not null" json:"partyBId"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"-"`

	// Computed fields.
	Stages      []ContractStage `gorm:"-" json:"stages"`
	PartyA      *User           `gorm:"foreignKey:PartyAID" json:"partyA"`
	PartyB      *User           `gorm:"foreignKey:PartyBID" json:"partyB"`
	Requirement *Requirement    `gorm:"foreignKey:RequirementID" json:"requirement"`
}

// BeforeSave serializes stages.
func (c *Contract) BeforeSave(_ *gorm.DB) error {
	if c.Stages != nil {
		raw, err := json.Marshal(c.Stages)
		if err != nil {
			return err
		}
		c.StagesJS = string(raw)
	}
	return nil
}

// AfterFind restores stages.
func (c *Contract) AfterFind(_ *gorm.DB) error {
	c.Stages = []ContractStage{}
	if c.StagesJS != "" {
		_ = json.Unmarshal([]byte(c.StagesJS), &c.Stages)
	}
	for i := range c.Stages {
		// Legacy data written before staged payments used "done" for paid stages.
		if c.Stages[i].Status == "done" {
			c.Stages[i].Status = "paid"
		}
	}
	return nil
}
