package data_controllers

import (
  "fmt"
  "net/http"
  "time"

  "github.com/julienschmidt/httprouter"
  "github.com/nttdots/go-dots/dots_server/db"
  "github.com/nttdots/go-dots/dots_server/models"
  "github.com/nttdots/go-dots/dots_server/models/data"
  log "github.com/sirupsen/logrus"
  types "github.com/nttdots/go-dots/dots_common/types/data"
  messages "github.com/nttdots/go-dots/dots_common/messages/data"
)

type PostController struct {
}

func (c *PostController) Post(customer *models.Customer, r *http.Request, p httprouter.Params) (Response, error) {
  now := time.Now()
  cuid := p.ByName("cuid")
  cdid := p.ByName("cdid")
  log.WithField("cuid", cuid).Info("[PostController] POST")

  // Check missing 'cuid'
  if cuid == "" {
    log.Error("Missing required path 'cuid' value.")
    return ErrorResponse(http.StatusBadRequest, ErrorTag_Missing_Attribute, "Missing a mandatory attribute : 'cuid'")
  }

  // Unmarshal
  ar := messages.PostRequest{}
  err := Unmarshal(r, &ar)
  if err != nil {
    errString := fmt.Sprintf("Invalid body data format: %+v", err)
    return ErrorResponse(http.StatusBadRequest, ErrorTag_Invalid_Value, errString)
  }

  log.Infof("[PostController] Post request=%#+v", ar)

  // Validation
  ir, err := ar.ValidateExtract(r.Method, customer)
  if err != nil {
    return ErrorResponse(http.StatusBadRequest, ErrorTag_Bad_Attribute, err.Error())
  }

  return WithTransaction(func (tx *db.Tx) (Response, error) {
    return WithClient(tx, customer, cuid, func (client *data_models.Client) (res Response, err error) {
      switch req := ir.(type) {
      case *messages.AliasesRequest:
        res, err = postAliases(tx, client, req, now)
      case *messages.ACLsRequest:
        res, err = postAcls(tx, client, customer, req, now)
      case *messages.VendorMappingRequest:
        res, err = postVendorMapping(tx, client, req, cdid)
      default:
        return responseOf(fmt.Errorf("Unexpected request: %#+v", req))
      }
      return
    })
  })
}

// Post aliases
func postAliases(tx *db.Tx, client *data_models.Client, req *messages.AliasesRequest, now time.Time) (Response, error) {
  var errMsg string
  n := []data_models.Alias{}
  aliasMap := []data_models.Alias{}
  for _,alias := range req.Aliases.Alias {
    e, err := data_models.FindAliasByName(tx, client, alias.Name, now)
    if err != nil {
      errMsg = fmt.Sprintf("Failed to get alias with 'name' = %+v", alias.Name)
      return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
    }
    if e != nil {
      errMsg = fmt.Sprintf("Specified alias 'name' (%v) is already registered", alias.Name)
      return ErrorResponse(http.StatusConflict, ErrorTag_Resource_Denied, errMsg)
    } else {
      alias.TargetPrefix = data_models.RemoveOverlapIPPrefix(alias.TargetPrefix)
      n = append(n, data_models.NewAlias(client, alias, now, defaultAliasLifetime))
    }
  }

  for _,alias := range n {
    err := alias.Save(tx)
    if err != nil {
      errMsg = fmt.Sprintf("Failed to save alias with 'name' = %+v", alias.Alias.Name)
      return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
    }
    aliasMap = append(aliasMap, alias)
  }

  for _,alias := range aliasMap {
    // Add alias to check expired
    data_models.AddActiveAliasRequest(alias.Id, alias.Client.Id, alias.Alias.Name, alias.ValidThrough)
  }
  return EmptyResponse(http.StatusCreated)
}

// Post acls
func postAcls(tx *db.Tx, client *data_models.Client, customer *models.Customer, req *messages.ACLsRequest, now time.Time) (Response, error) {
  var errMsg string
  n := []data_models.ACL{}
  for _,acl := range req.ACLs.ACL {
    e, err := data_models.FindACLByName(tx, client, acl.Name, now)
    if err != nil {
      errMsg = fmt.Sprintf("Failed to get acl with 'name' = %+v", acl.Name)
      return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
    }
    if e != nil {
      errMsg = fmt.Sprintf("Specified acl 'name'(%v) is already registered", acl.Name)
      return ErrorResponse(http.StatusConflict, ErrorTag_Resource_Denied, errMsg)
    } else {
      if acl.ActivationType == nil {
        defValue := types.ActivationType_ActivateWhenMitigating
        acl.ActivationType = &defValue
      }
      if acl.ACEs.ACE != nil {
        for _,ace := range acl.ACEs.ACE {
          if ace.Matches.IPv4 != nil && ace.Matches.IPv4.Fragment != nil && ace.Matches.IPv4.Fragment.Operator == nil {
            defValue := types.Operator_MATCH
            ace.Matches.IPv4.Fragment.Operator = &defValue
          } else if ace.Matches.IPv6 != nil && ace.Matches.IPv6.Fragment != nil && ace.Matches.IPv6.Fragment.Operator == nil {
            defValue := types.Operator_MATCH
            ace.Matches.IPv6.Fragment.Operator = &defValue
          }
          if ace.Matches.TCP != nil && ace.Matches.TCP.FlagsBitmask != nil && ace.Matches.TCP.FlagsBitmask.Operator == nil {
            defValue := types.Operator_MATCH
            ace.Matches.TCP.FlagsBitmask.Operator = &defValue
          }
        }
      }
      n = append(n, data_models.NewACL(client, acl, now, defaultACLLifetime))
    }
  }

  acls := []data_models.ACL{}
  aclMap := []data_models.ACL{}
  for _,acl := range n {
    err := acl.Save(tx)
    if err != nil {
      errMsg = fmt.Sprintf("Failed to save acl with 'name' = %+v", acl.ACL.Name)
      return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
    }

    // Handle ACL activation type
    isActive, err := acl.IsActive()
    if err != nil {
      errMsg = fmt.Sprintf("Failed to check acl status with acl 'name' = %+v", acl.ACL.Name)
      return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
    }
    if isActive {
      acls = append(acls, acl)
    }
    aclMap = append(aclMap, acl)
  }

  // Call blocker
  err := data_models.CallBlocker(acls, customer.Id)
  if err != nil{
    log.Errorf("Data channel POST ACL. CallBlocker is error: %s\n", err)
    // handle error when call blocker failed
    // Rollback
    for _, acl := range acls {
      data_models.DeleteACLByName(tx, client.Id, acl.ACL.Name, now)
      errMsg = fmt.Sprintf("Failed to call blocker with acl 'name' = %+v", acl.ACL.Name)
    }
    return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
  }
  for _,acl := range aclMap {
    // Add acl to check expired
    data_models.AddActiveACLRequest(acl.Id,acl.Client.Id, acl.ACL.Name, acl.ValidThrough)
  }
  return EmptyResponse(http.StatusCreated)
}

// Post vendor-mapping
func postVendorMapping(tx *db.Tx, client *data_models.Client, req *messages.VendorMappingRequest, cdid string) (Response, error) {
  var errMsg string
  if client != nil && client.Cdid == nil && cdid != "" {
    errMsg = fmt.Sprintf("Dots server has not maintain a 'cdid' for client with cuid = %+v", client.Cuid)
    return ErrorResponse(http.StatusForbidden, ErrorTag_Access_Denied, errMsg)
  }
  errMsg = messages.ValidateVendorMapping(req)
  if errMsg != "" {
    log.Errorf(errMsg)
    return ErrorResponse(http.StatusBadRequest, ErrorTag_Missing_Attribute, errMsg)
  }
  vendors := []data_models.Vendor{}
  for _, v := range req.VendorMapping.Vendor {
    e, err := data_models.FindVendorByVendorId(tx, client.Id, int(*v.VendorId))
    if err != nil {
      errMsg = fmt.Sprintf("Failed to get vendor with 'vendor-id' = %+v. Error: %+v", *v.VendorId, err)
      log.Errorf(errMsg)
      return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
    }
    if e.Id > 0 {
      errMsg = fmt.Sprintf("Specified vendor 'vendor-id' (%v) is already registered", *v.VendorId)
      log.Errorf(errMsg)
      return ErrorResponse(http.StatusConflict, ErrorTag_Resource_Denied, errMsg)
    }
    vendor := data_models.NewVendorMapping(client.Id, v)
    vendors = append(vendors, vendor)
  }
  for _, vendor := range vendors {
    err := vendor.Save(tx)
    if err != nil {
      errMsg = fmt.Sprintf("Failed to save vendor-mapping with 'vendor-id' = %+v", vendor.VendorId)
      log.Errorf(errMsg)
      return ErrorResponse(http.StatusInternalServerError, ErrorTag_Operation_Failed, errMsg)
    }
  }
  return EmptyResponse(http.StatusCreated)
}
