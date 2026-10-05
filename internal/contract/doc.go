// Package contract is the contract suite with the real MusicLib (DESIGN.md
// §12.3, step S23): the scenarios A1–A20, run against the published MusicLib
// and the runtime image of Vibrance in the Compose project
// vibrance-contract. MusicLib is changed through its documented API and
// Vibrance is checked through its own.
//
// Its files carry the build tag `contract`: they run only in the `suite`
// container of scripts/contract.sh, which also does what only Docker can do
// when the suite asks. The gate vets them with `-tags contract` and never
// runs them.
package contract
