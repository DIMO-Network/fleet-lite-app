/** A tenant's fleet group as returned by the /fleet/groups endpoints. */
export interface FleetGroup {
    id: string;
    name: string;
    color: string;
    /** Number of vehicles in the group (present on list/get responses). */
    vehicleCount?: number;
    createdAt: string;
    updatedAt: string;
}

export interface FleetGroupsResponse {
    groups: FleetGroup[];
}
