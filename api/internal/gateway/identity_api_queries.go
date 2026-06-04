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

// VehiclesByPrivilegeAndCursorQuery — list vehicles a developer-license client ID
// is privileged on (SACD-shared), paginated by cursor.
// Format args: 1=clientID, 2=first (int), 3=after-cursor (quoted string or `null`).
const VehiclesByPrivilegeAndCursorQuery = `{
	vehicles(filterBy: {privileged: "%s"}, first: %d, after: %s) {
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

// DeveloperLicenseByClientIDQuery — resolve a developer license's redirect URIs by client ID.
// Format args: 1=clientID.
const DeveloperLicenseByClientIDQuery = `{
	developerLicense(by: {clientId: "%s"}) {
		tokenId
		owner
		alias
		redirectURIs(first: 10) {
			edges {
				node {
					uri
				}
			}
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
