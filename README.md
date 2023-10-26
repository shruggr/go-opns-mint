# go-opns-mint

`go run .`

`GET http://localhost:8080/mine/:char/:pow`
 - `:char` = ASCII character to be mined
 - `:pow` = Contract `pow` value as hex

Returns: 
```
{
  "nonce": string, // hex
  "hashes": number // number of hashes calculated
}
```
