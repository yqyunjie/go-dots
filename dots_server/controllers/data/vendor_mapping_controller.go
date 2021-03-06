package data_controllers

import (
	"fmt"
	"strconv"
	"net/http"
  
	"github.com/julienschmidt/httprouter"
	"github.com/nttdots/go-dots/dots_server/db"
	"github.com/nttdots/go-dots/dots_server/models"
	"github.com/nttdots/go-dots/dots_server/models/data"
	log "github.com/sirupsen/logrus"
	types    "github.com/nttdots/go-dots/dots_common/types/data"
	messages "github.com/nttdots/go-dots/dots_common/messages/data"
  )

type VendorMappingController struct {}

// Get vendor-mapping
func (v *VendorMappingController) Get(customer *models.Customer, r *http.Request, p httprouter.Params) (Response, error) {
	cuid := p.ByName("cuid")
	log.WithField("cuid", cuid).Info("[VendorMappingController] GET")
  
	// Check missing 'cuid'
	if cuid == "" {
		errMsg := "Missing required path 'cuid' value."
	    log.Error(errMsg)
	    return ErrorResponse(http.StatusBadRequest, ErrorTag_Missing_Attribute, errMsg)
	}
	return WithTransaction(func (tx *db.Tx) (Response, error) {
		return WithClient(tx, customer, cuid, func (client *data_models.Client) (_ Response, err error) {
			return findVendorMapping(tx, &client.Id, client.Cuid, r)
		})
	})
}

// Get vendor-mapping of sever
func (v *VendorMappingController) GetVendorMappingOfServer(customer *models.Customer, r *http.Request, p httprouter.Params) (Response, error) {
	log.Info("[VendorMappingController] GET")
	capabilities := getCapabilities()
	if *capabilities.Capabilities.VendorMappingEnabled == false {
		errMsg := "'vendor-mapping-enabled' is set to 'false'. Failed to Get the Dots server's vendor attack mapping details."
		log.Error(errMsg)
	    return ErrorResponse(http.StatusBadRequest, ErrorTag_Bad_Attribute, errMsg)
	}
	return WithTransaction(func (tx *db.Tx) (Response, error) {
		return findVendorMapping(tx, nil, "", r)
	})
}

// Put vendor-mapping
func (vc *VendorMappingController) Put(customer *models.Customer, r *http.Request, p httprouter.Params) (Response, error) {
	var errMsg string
	cuid := p.ByName("cuid")
	vendorId := p.ByName("vendorId")
	log.WithField("cuid", cuid).Info("[VendorMappingController] PUT")
	// Check missing 'cuid'
	if cuid == "" {
		errMsg = "Missing required path 'cuid' value."
		log.Error(errMsg)
		return ErrorResponse(http.StatusBadRequest, ErrorTag_Missing_Attribute, errMsg)
	}
	if vendorId == "" {
		errMsg = "Missing required path 'vendor-id' value."
		log.Error(errMsg)
	    return ErrorResponse(http.StatusBadRequest, ErrorTag_Missing_Attribute, errMsg)
	}
	req := messages.VendorMappingRequest{}
	err := Unmarshal(r, &req)
	if err != nil {
		errMsg = fmt.Sprintf("Invalid body data format: %+v", err)
		log.Error(errMsg)
		return ErrorResponse(http.StatusBadRequest, ErrorTag_Invalid_Value, errMsg)
	}
	// Validate body data
	vId, err := strconv.Atoi(vendorId)
	if err != nil {
		errMsg := "Failed to convert 'vendor-id' to int"
		log.Errorf(errMsg)
		return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
	}
	errMsg = messages.ValidateWithVendorId(vId, &req)
	if errMsg != "" {
		log.Errorf(errMsg)
		return ErrorResponse(http.StatusBadRequest, ErrorTag_Bad_Attribute, errMsg)
	}
	errMsg = messages.ValidateVendorMapping(&req)
	if errMsg != "" {
		log.Errorf(errMsg)
		return ErrorResponse(http.StatusBadRequest, ErrorTag_Missing_Attribute, errMsg)
	}
	return WithTransaction(func (tx *db.Tx) (Response, error) {
		return WithClient(tx, customer, cuid, func (client *data_models.Client) (_ Response, err error) {
			// Find vendor-mapping by vendor-id
			e, err := data_models.FindVendorByVendorId(tx, client.Id, vId)
			if err != nil {
				errMsg = fmt.Sprintf("Failed to get vendor with 'vendor-id' = %+v. Error: %+v", vId, err)
				log.Errorf(errMsg)
				return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
			}
			if e.Id == 0 {
				errMsg := fmt.Sprintf("Not Found vendor-mapping by specified vendor-id = %+v", vId)
				log.Errorf(errMsg)
				return ErrorResponse(http.StatusNotFound, ErrorTag_Invalid_Value, errMsg)
			}
			// Save attack-detail
			err = e.Save(tx)
			if err != nil {
				errMsg = fmt.Sprintf("Failed to save vendor-mapping with vendor-id = %+v", vId)
				log.Errorf(errMsg)
				return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
			}
			return EmptyResponse(http.StatusNoContent)
		})
	})
}

// Find vendor-mapping
func findVendorMapping(tx *db.Tx, clientId *int64, cuid string, r *http.Request) (Response, error) {
	// Find vendor-mapping by client_id
	vendorList, err := data_models.FindVendorMappingByClientId(tx, clientId)
	if err != nil {
		return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, err.Error())
	}
	if len(vendorList) < 1 {
		errMsg := fmt.Sprintf("Not Found vendor-mapping by specified dots-client = %+v", cuid)
		log.Errorf(errMsg)
		return ErrorResponse(http.StatusNotFound, ErrorTag_Invalid_Value, errMsg)
	}
	q := r.URL.Query()
	var depth *int
	  if a, ok := q["depth"]; ok {
		tmpDepth, err := strconv.Atoi(a[0])
		if err != nil {
			errMsg := "Failed to convert 'depth' to int"
			log.Error(errMsg)
			return ErrorResponse(http.StatusBadRequest, ErrorTag_Invalid_Value, errMsg)
		}
		depth = & tmpDepth
	} else {
		depth = nil
	}
	tv := vendorList.ToTypesVendorMapping(depth)
	s := messages.VendorMappingResponse{}
	s.VendorMapping = *tv
	cp, err := messages.ContentFromRequest(r)
	if err != nil {
		return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, err.Error())
	}
	m, err := messages.ToMap(s, cp)
	if err != nil {
		return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, err.Error())
	}
	return YangJsonResponse(m)
}

// Get vendor-mapping by cuid
func GetVendorMappingByCuid(customer *models.Customer, cuid string) (res *types.VendorMapping, err error) {
	_, err = WithTransaction(func (tx *db.Tx) (Response, error) {
		return WithClient(tx, customer, cuid, func (client *data_models.Client) (_ Response, err error) {
			// Find vendor-mapping by client_id
			vendorList, err := data_models.FindVendorMappingByClientId(tx, &client.Id)
			if err != nil {
				return
			}
			res = vendorList.ToTypesVendorMapping(nil)
			return
		})
	})
	return
}