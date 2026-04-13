package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://api.steampowered.com"

var PersonaStateMap = map[int]string{
	0: "离线",
	1: "在线",
	2: "忙碌",
	3: "离开",
	4: "打盹",
	5: "想交易",
	6: "想玩游戏",
}

type Client struct {
	apiKey string
	http   *http.Client
}

type Player struct {
	SteamID      string `json:"steamid"`
	PersonaName  string `json:"personaname"`
	PersonaState int    `json:"personastate"`
	GameExtra    string `json:"gameextrainfo"`
	GameID       string `json:"gameid"`
	AvatarFull   string `json:"avatarfull"`
	ProfileURL   string `json:"profileurl"`
	LastLogoff   int64  `json:"lastlogoff"`
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Client) ResolveSteamID64(ctx context.Context, input string) (string, error) {
	if _, err := strconv.ParseUint(input, 10, 64); err == nil {
		return input, nil
	}

	var response struct {
		Response struct {
			Success int    `json:"success"`
			SteamID string `json:"steamid"`
		} `json:"response"`
	}

	if err := c.getJSON(ctx, "/ISteamUser/ResolveVanityURL/v1/", map[string]string{
		"key":       c.apiKey,
		"vanityurl": input,
	}, &response); err != nil {
		return "", err
	}

	if response.Response.Success != 1 || response.Response.SteamID == "" {
		return "", fmt.Errorf("无法将该标识解析为 SteamID64，请确认自定义主页地址是否正确")
	}

	return response.Response.SteamID, nil
}

func (c *Client) GetFriendIDs(ctx context.Context, steamID64 string) ([]string, error) {
	var response struct {
		FriendsList struct {
			Friends []struct {
				SteamID string `json:"steamid"`
			} `json:"friends"`
		} `json:"friendslist"`
	}

	if err := c.getJSON(ctx, "/ISteamUser/GetFriendList/v1/", map[string]string{
		"key":          c.apiKey,
		"steamid":      steamID64,
		"relationship": "friend",
	}, &response); err != nil {
		return nil, err
	}

	friendIDs := make([]string, 0, len(response.FriendsList.Friends))
	for _, friend := range response.FriendsList.Friends {
		if friend.SteamID != "" {
			friendIDs = append(friendIDs, friend.SteamID)
		}
	}

	return friendIDs, nil
}

func (c *Client) GetPlayerSummaries(ctx context.Context, steamIDs []string) ([]Player, error) {
	if len(steamIDs) == 0 {
		return nil, nil
	}

	players := make([]Player, 0, len(steamIDs))
	for _, batch := range chunk(steamIDs, 100) {
		var response struct {
			Response struct {
				Players []Player `json:"players"`
			} `json:"response"`
		}

		if err := c.getJSON(ctx, "/ISteamUser/GetPlayerSummaries/v2/", map[string]string{
			"key":      c.apiKey,
			"steamids": strings.Join(batch, ","),
		}, &response); err != nil {
			return nil, err
		}

		players = append(players, response.Response.Players...)
	}

	return players, nil
}

func (c *Client) getJSON(ctx context.Context, path string, params map[string]string, out any) error {
	query := url.Values{}
	for key, value := range params {
		query.Set(key, value)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("steam api request failed: %s", resp.Status)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func PersonaStateText(code int) string {
	if text, ok := PersonaStateMap[code]; ok {
		return text
	}
	return fmt.Sprintf("未知状态(%d)", code)
}

func chunk(items []string, size int) [][]string {
	if len(items) == 0 {
		return nil
	}

	var result [][]string
	for start := 0; start < len(items); start += size {
		end := start + size
		if end > len(items) {
			end = len(items)
		}
		result = append(result, items[start:end])
	}
	return result
}
