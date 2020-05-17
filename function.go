package functions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/go-chi/render"
	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/sheets/v4"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var proxyURL = "https://ubsapradishproxy.azurewebsites.net/api"

func proxySprintf(pattern string, a ...interface{}) string {
	return fmt.Sprintf(proxyURL+pattern, a...)
}

func getSheetsService() (*sheets.Service, error) {
	data, err := ioutil.ReadFile("./credentials.json")
	if err != nil {
		return nil, err
	}
	conf, err := google.JWTConfigFromJSON(data, "https://www.googleapis.com/auth/spreadsheets")
	if err != nil {
		return nil, err
	}

	client := conf.Client(context.Background())

	return sheets.New(client)
}

func getOrderItemsCount(srv *sheets.Service, spreadsheetID string) (count int, err error) {
	readRange := "Order_Items!A2:C"
	respRead, err := srv.Spreadsheets.Values.Get(spreadsheetID, readRange).Do()
	if err != nil {
		return 0, err
	}

	return len(respRead.Values), nil
}

func appendOrderItem(srv *sheets.Service, spreadsheetID string, rfpId string, o orderItem) (err error) {
	var appendValues sheets.ValueRange

	appendValues.Values = append(appendValues.Values, []interface{}{rfpId, strconv.Itoa(o.OrderItemID), o.SKUBuyer, fmt.Sprintf("%f", o.Quantity), o.Unit})

	_, err = srv.Spreadsheets.Values.Append(spreadsheetID, "Order_Items!A2", &appendValues).ValueInputOption("RAW").Do()
	if err != nil {
		log.Fatalf("Unable to write orderItems data to sheet: %v", err)
		return err
	}
	return nil
}

func appendRfp(srv *sheets.Service, spreadsheetID string, rfp rfpResponse) (err error) {
	var appendValues sheets.ValueRange

	appendValues.Values = append(appendValues.Values, []interface{}{rfp.RequestForProposalID, "Buyer", strconv.Itoa(len(rfp.Items)), rfp.LatestDeliveryDate})

	_, err = srv.Spreadsheets.Values.Append(spreadsheetID, "RFPS!A2", &appendValues).ValueInputOption("RAW").Do()
	if err != nil {
		log.Fatalf("Unable to write RFP data to sheet: %v", err)
		return err
	}
	return nil
}

type orderItem struct {
	OrderItemID int     `json:"orderItemId"`
	SKUBuyer    string  `json:"skuBuyer"`
	Quantity    float32 `json:"quantity"`
	Unit        string  `json:"unit"`
}

type rfpResponse struct {
	RequestForProposalID string      `json:"requestForProposalId"`
	SupplierID           string      `json:"supplierId"`
	Items                []orderItem `json:"items"`
	LatestDeliveryDate   string      `json:"latestDeliveryDate"`
}

func insertRFP(srv *sheets.Service, spreadsheetID string, bRFP baselineRFP) (orderItemsIncrease int, err error) {

	resp, err := http.Get(proxySprintf("/RequestForProposals/%v", bRFP.ID))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	var rfp rfpResponse
	err = decoder.Decode(&rfp)
	if err != nil {
		return 0, err
	}

	for _, o := range rfp.Items {
		err = appendOrderItem(srv, spreadsheetID, bRFP.ID, o)
		if err != nil {
			return 0, err
		}
	}

	err = appendRfp(srv, spreadsheetID, rfp)
	if err != nil {
		return 0, err
	}

	return len(rfp.Items), nil
}

type baselineRFP struct {
	ID                 string `json:"requestForProposalId"`
	BuyerProductId     string `json:"buyerId"`
	SupplierProductId  string `json:"supplierId"`
	LatestDeliveryDate string `json:"latestDeliveryDate"`
}

func updateIncommingRFPs(srv *sheets.Service, spreadsheetID string) (orderItemsIncrease, rfpsIncrease int, err error) {
	readRange := "RFPS!A2"
	respRead, err := srv.Spreadsheets.Values.Get(spreadsheetID, readRange).Do()
	if err != nil {
		return 0, 0, err
	}

	resp, err := http.Get(proxySprintf("/RequestForProposals"))
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	var rfps []baselineRFP
	err = decoder.Decode(&rfps)
	if err != nil {
		return 0, 0, err
	}

	savedRfpsCount := len(respRead.Values)
	totalRfpsCount := len(rfps)

	if totalRfpsCount > savedRfpsCount {
		log.Printf("Ok, lets start inserting")
		orderItemsIncrease = 0
		newRfps := rfps[savedRfpsCount:]
		for _, rfp := range newRfps {
			newOrderCount, err := insertRFP(srv, spreadsheetID, rfp)
			if err != nil {
				return 0, 0, err
			}
			orderItemsIncrease += newOrderCount
		}
		return orderItemsIncrease, totalRfpsCount - savedRfpsCount, nil
	}

	return 0, 0, nil
}

type baselineSku struct {
	ID                string `json:"id"`
	ProductName       string `json:"ProductName"`
	BuyerProductId    string `json:"BuyerProductId"`
	SupplierProductId string `json:"SupplierProductId"`
}

type baselinePriceScale struct {
	Sku          baselineSku `json:Sku`
	QuantityFrom int         `json:"QuantityFrom"`
	QuantityTo   int         `json:"QuantityTo"`
	Price        float32     `json:"Price"`
	Unit         string      `json:"Unit"`
	Currency     string      `json:"Currency"`
}

type baselineProposal struct {
	ProposalID      string               `json:"ProposalId"`
	BuyerID         string               `json:"BuyerId"`
	ReferencedRfpID string               `json:"ReferencedRfpId"`
	PriceScales     []baselinePriceScale `json:"priceScales"`
}

func getSKU(skuID string, skus [][]interface{}, buyerId string) (sku baselineSku, err error) {
	for _, row := range skus {
		if row[0] != skuID {
			continue
		}

		return baselineSku{fmt.Sprintf("%s", row[0]), fmt.Sprintf("%s", row[1]), buyerId, fmt.Sprintf("%s", row[0])}, nil
	}

	return sku, fmt.Errorf("Could not find sku with id %v", skuID)
}

func getPriceScale(priceScaleID string, priceScales [][]interface{}, skus [][]interface{}) (p baselinePriceScale, err error) {
	for _, row := range priceScales {
		if row[0] != priceScaleID {
			continue
		}

		sku, err := getSKU(fmt.Sprintf("%s", row[1]), skus, fmt.Sprintf("%s", row[7]))
		if err != nil {
			return p, err
		}

		quantityFrom, err := strconv.Atoi(fmt.Sprintf("%s", row[2]))
		if err != nil {
			return p, err
		}

		quantityTo, err := strconv.Atoi(fmt.Sprintf("%s", row[3]))
		if err != nil {
			return p, err
		}

		price, err := strconv.ParseFloat(fmt.Sprintf("%s", row[5]), 32)
		if err != nil {
			return p, err
		}

		p = baselinePriceScale{sku, quantityFrom, quantityTo, float32(price), fmt.Sprintf("%s", row[4]), fmt.Sprintf("%s", row[6])}

		return p, nil
	}
	return p, fmt.Errorf("Could not find price scale with id %v", priceScaleID)
}

func createProposal(proposal []interface{}, priceScales [][]interface{}, skus [][]interface{}) (b *baselineProposal, err error) {
	scaleIds := strings.Split(fmt.Sprintf("%s", proposal[3]), ",")
	proposalPriceScales := make([]baselinePriceScale, len(scaleIds))
	for i, k := range scaleIds {
		proposalPriceScales[i], err = getPriceScale(k, priceScales, skus)
		if err != nil {
			return b, err
		}
	}
	b = &baselineProposal{fmt.Sprintf("%s", proposal[0]), fmt.Sprintf("%s", proposal[1]), fmt.Sprintf("%s", proposal[2]), proposalPriceScales}
	return b, nil
}

func sendProposal(proposal *baselineProposal) error {
	jsonValue, _ := json.Marshal(proposal)

	resp, err := http.Post(proxySprintf("/Proposals"), "application/json", bytes.NewBuffer(jsonValue))
	defer resp.Body.Close()
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("Posting proposal returned status code %v", resp.Status)
	}
	return nil
}

func markProposalSent(proposalID string, srv *sheets.Service, spreadsheetID string) error {
	proposalIDInt, err := strconv.Atoi(proposalID)
	if err != nil {
		return err
	}

	proposalRow := proposalIDInt + 1
	updateRange := fmt.Sprintf("Proposals!E%v:E%v", proposalRow, proposalRow)

	var updateValues sheets.ValueRange

	updateValues.Values = append(updateValues.Values, []interface{}{"Yes"})

	_, err = srv.Spreadsheets.Values.Update(spreadsheetID, updateRange, &updateValues).ValueInputOption("RAW").Do()
	if err != nil {
		log.Fatalf("Unable to write update proposal data to sheet: %v", err)
		return err
	}
	return nil

}

type proposalResponse struct {
	ProposalID string `json:"proposalId"`
}

func sendOutgoingProposals(srv *sheets.Service, spreadsheetID string) (sentProposals int, err error) {
	skusReadRange := "SKU!A2:Z"
	skusRead, err := srv.Spreadsheets.Values.Get(spreadsheetID, skusReadRange).Do()
	if err != nil {
		return sentProposals, err
	}

	priceScalesRange := "Proposal_Tiers!A2:Z"
	priceScalesRead, err := srv.Spreadsheets.Values.Get(spreadsheetID, priceScalesRange).Do()
	if err != nil {
		return sentProposals, err
	}

	proposalsReadRange := "Proposals!A2:Z"
	proposalsRead, err := srv.Spreadsheets.Values.Get(spreadsheetID, proposalsReadRange).Do()
	if err != nil {
		return sentProposals, err
	}

	resp, err := http.Get(proxySprintf("/Proposals"))
	if err != nil {
		return sentProposals, err
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	var proposals []proposalResponse
	err = decoder.Decode(&proposals)
	if err != nil {
		return sentProposals, err
	}

	savedProposalsCount := len(proposalsRead.Values)
	sentProposalsCount := len(proposals)

	if savedProposalsCount > sentProposalsCount {
		log.Printf("Ok, lets start sending")
		newProposals := proposalsRead.Values[sentProposalsCount:]
		for _, row := range newProposals {
			if row[4] != "No" {
				continue
			}

			proposal, err := createProposal(row, priceScalesRead.Values, skusRead.Values)
			if err != nil {
				return sentProposals, err
			}

			sendProposal(proposal)
			if err != nil {
				return sentProposals, err
			}
			err = markProposalSent(proposal.ProposalID, srv, spreadsheetID)
			if err != nil {
				return sentProposals, err
			}

			sentProposals++
		}
	}

	return sentProposals, nil
}

type updates struct {
	OrderItems    int `json:"newOrderItems"`
	Rfp           int `json:"newRFPs"`
	SentProposals int `json:"sentProposals"`
}

type updateResponse struct {
	APIResponse
	Updates updates `json:"updates,omitempty"`
}

func UpdateIncoming(w http.ResponseWriter, r *http.Request) {

	srv, err := getSheetsService()

	if err != nil {
		log.Fatalf("Unable to retrieve Sheets client: %v", err)
	}

	spreadsheetID := "1Z_DonR4P5T5xyjKODgDyyF3BgQG7eObodl8d1IWxS6s"

	orderItemsIncrease, rfpsIncrease, err := updateIncommingRFPs(srv, spreadsheetID)
	if err != nil {
		log.Fatalf("response %v\n", err)
		render.JSON(w, r, updateResponse{APIResponse{false, err.Error()}, updates{0, 0, 0}})
		return
	}

	render.JSON(w, r, updateResponse{APIResponse{true, ""}, updates{orderItemsIncrease, rfpsIncrease, 0}})

}

func SendProposals(w http.ResponseWriter, r *http.Request) {

	srv, err := getSheetsService()

	if err != nil {
		log.Fatalf("Unable to retrieve Sheets client: %v", err)
	}

	spreadsheetID := "1Z_DonR4P5T5xyjKODgDyyF3BgQG7eObodl8d1IWxS6s"

	sentProposals, err := sendOutgoingProposals(srv, spreadsheetID)
	if err != nil {
		log.Fatalf("response %v\n", err)
		render.JSON(w, r, updateResponse{APIResponse{false, err.Error()}, updates{0, 0, 0}})
		return
	}

	render.JSON(w, r, updateResponse{APIResponse{true, ""}, updates{0, 0, sentProposals}})
}

func Authenticate(w http.ResponseWriter, r *http.Request) {

	emails, ok := r.URL.Query()["email"]

	if !ok || len(emails[0]) < 1 {
		log.Println("Url Param 'email' is missing")
		render.JSON(w, r, APIResponse{false, "Url Param 'email' is missing"})
		return
	}

	email := emails[0]

	passwords, ok := r.URL.Query()["password"]

	if !ok || len(passwords[0]) < 1 {
		log.Println("Url Param 'password' is missing")
		render.JSON(w, r, APIResponse{false, "Url Param 'password' is missing"})
		return
	}

	password := passwords[0]

	params := url.Values{}
	params.Add("email", email)
	params.Add("password", password)

	fmt.Println(params.Encode())

	resp, err := http.Post(proxySprintf("/Authentication?%s", params.Encode()), "application/json", bytes.NewBufferString(""))
	defer resp.Body.Close()
	if err != nil {
		render.JSON(w, r, APIResponse{false, err.Error()})
		return
	}

	if resp.StatusCode != 200 {
		render.JSON(w, r, APIResponse{false, fmt.Sprintf("Error authenticating, response code %v", resp.Status)})
		return
	}

	render.JSON(w, r, APIResponse{true, ""})
}

func UpdateAll(w http.ResponseWriter, r *http.Request) {

	srv, err := getSheetsService()

	if err != nil {
		log.Fatalf("Unable to retrieve Sheets client: %v", err)
	}

	spreadsheetID := "1Z_DonR4P5T5xyjKODgDyyF3BgQG7eObodl8d1IWxS6s"

	orderItemsIncrease, rfpsIncrease, err := updateIncommingRFPs(srv, spreadsheetID)
	if err != nil {
		log.Fatalf("response %v\n", err)
		render.JSON(w, r, updateResponse{APIResponse{false, err.Error()}, updates{0, 0, 0}})
		return
	}

	sentProposals, err := sendOutgoingProposals(srv, spreadsheetID)
	if err != nil {
		log.Fatalf("response %v\n", err)
		render.JSON(w, r, updateResponse{APIResponse{false, err.Error()}, updates{0, 0, 0}})
		return
	}

	render.JSON(w, r, updateResponse{APIResponse{true, ""}, updates{orderItemsIncrease, rfpsIncrease, sentProposals}})

}
