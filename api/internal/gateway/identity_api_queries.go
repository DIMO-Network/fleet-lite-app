package gateway

// VehiclesByWalletAndCursorQuery — list vehicles owned by a wallet, paginated by cursor.
// Format args: 1=owner wallet, 2=after-cursor (quoted string or `null`).
const VehiclesByWalletAndCursorQuery = `{
	vehicles(filterBy: {owner: "%s"}, first: 100, after: %s) {
		nodes {
			id
			tokenId
			mintedAt
			owner
			definition {
				id
				make
				model
				year
			}
			syntheticDevice {
				id
				tokenId
				mintedAt
			}
			aftermarketDevice {
				tokenId
				serial
				imei
			}
		}
		pageInfo {
			hasPreviousPage
			hasNextPage
			startCursor
			endCursor
		}
	}
}`

// VehicleByTokenIDQuery — fetch a single vehicle by tokenId.
const VehicleByTokenIDQuery = `{
	vehicle(tokenId: %s) {
		id
		tokenId
		mintedAt
		owner
		definition {
			id
			make
			model
			year
		}
		syntheticDevice {
			id
			tokenId
			mintedAt
		}
		aftermarketDevice {
			tokenId
			serial
			imei
		}
	}
}`
