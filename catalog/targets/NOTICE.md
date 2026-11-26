# Target catalogs

Copied verbatim from https://github.com/dkorunic/astro-recommender (internal/catalog/targets/*.yaml)
at commit 2a60d8ad0ba37df2eb8c0f0a388371ddb0cc068b (2026-10-20). Do not edit here; re-copy to update.

astro-recommender is MIT licensed:

    The MIT License
    
    Copyright (c) 2022  Dinko Korunic
    
    Permission is hereby granted, free of charge, to any person obtaining a copy
    of this software and associated documentation files (the "Software"), to deal
    in the Software without restriction, including without limitation the rights
    to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
    copies of the Software, and to permit persons to whom the Software is
    furnished to do so, subject to the following conditions:
    
    The above copyright notice and this permission notice shall be included in
    all copies or substantial portions of the Software.
    
    THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
    IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
    FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
    AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
    LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
    OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
    THE SOFTWARE.
    

## Catalog credits (from astro-recommender README)

- Planning ideas (airmass weighting, sky-brightness scoring, swap-optimised scheduling) and the extinction model (Rayleigh scattering scaled by elevation, ozone, aerosol from the optical depth) from [astrogo](https://github.com/TuSKan/astrogo) by TuSKan (MIT).
- [uptonight](https://github.com/mawinkler/uptonight) by Markus Winkler (MIT): the observability rules, the eight uptonight target lists in `internal/catalog/targets/`, comet selection from MPC orbital elements with a magnitude limit, and the local horizon as azimuth/altitude pairs. The default `GaryImm.yaml` is based on Gary Imm's top astrophotography targets and his Deep Sky Compendium. Ten of its entries were corrected against the 2026 (6th) edition of the Compendium. Seven sizes were plainly wrong; for example the Soul Nebula was listed at 1.8′ instead of 90′. Three positions had been updated in the new edition. NGC 1788 is typed a reflection nebula, as in the Compendium. Herschel400's NGC 2232, 2281, 6426 and 6882 had a mistyped coordinate (10–16′ off) and NGC 7000 the type `Nebula`; both are corrected against SIMBAD and the Compendium. `fillmag.py` replaces the central-star magnitudes uptonight's lists give planetary nebulae (M 57 15.8 for the nebula's 8.8).
- Weather ideas from [astro-forecast](https://github.com/markusschierz/astro-forecast) by Markus Schierz: the precipitation gate on the sky factor, and an upper-air seeing estimate from Open-Meteo's pressure levels for hours 7Timer does not cover (its wind shear, jet and Richardson number index was this project's first estimate, since replaced by the calibrated Dewan Cn² model).
- Moonlight details from [thorsky](https://github.com/jrthorstensen/thorsky) by John Thorstensen (BSD-2-Clause), the Python port of his skycalc: the opposition surge on the Moon's brightness (×1.35 at full, tapering linearly to none at 7°) and the Moon's brightness scaled by its distance relative to the mean 60.27 Earth radii.
- Accuracy reference: [gofa](https://github.com/hebl/gofa) by HE Boliang (MIT), the Go port of the IAU's [SOFA](http://iausofa.org) library. `TestSOFA`, `TestSOFAGalactic` and `TestScoreSOFA` check the hand-rolled Sun, Moon, sidereal time, precession, Earth position, galactic coordinates and altitude formulas against it (test-only; not linked into the binary).
- [astroplan](https://github.com/astropy/astroplan) by the Astropy Project (BSD-3-Clause), on which uptonight is built: the observability model (altitude, airmass and Moon-separation constraints checked per time step, and the fraction of time observable that uptonight's `FOTO` and our UP report) and the topocentric Moon.
- `GaryImmFull.yaml`: Gary Imm's Deep Sky Compendium (2026, 6th edition, revision 6b), data © Gary Imm. The spreadsheet states no redistribution terms.
- `OpenNGC.yaml` and `OpenIC.yaml` derive from [OpenNGC](https://github.com/mattiaverga/OpenNGC) by Mattia Verga, licensed [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/).
- `MWSC.yaml`: Milky Way Star Clusters, Kharchenko et al. 2013, A&A 558, A53, from CDS/VizieR catalog [J/A+A/558/A53](https://cdsarc.cds.unistra.fr/viz-bin/cat/J/A+A/558/A53).
- `Melotte.yaml`: Melotte (1915), Mem. RAS 60, 175; NGC/IC cross-identifications from Wikipedia's [Melotte catalogue](https://en.wikipedia.org/wiki/Melotte_catalogue) (CC BY-SA 4.0), Mel 31 and Mel 186 from [SIMBAD](https://simbad.cds.unistra.fr/).
- `Collinder.yaml`: Collinder (1931), Lund Medd. Ser. II 60; NGC/IC cross-identifications from Wikipedia's [Collinder catalogue](https://en.wikipedia.org/wiki/Collinder_catalogue) (CC BY-SA 4.0), 16 clusters from Dias et al. (2002, CDS/VizieR [B/ocl](https://cdsarc.cds.unistra.fr/viz-bin/cat/B/ocl)) and SIMBAD.
- `PNnet.yaml`: [Planetary Nebulae.net](https://planetarynebulae.net/), © Planetary Nebulae net, all rights reserved, included with the authors' permission. See also Le Dû et al. 2022, A&A 666, A152.
- `HASH.yaml`: This research has made use of the HASH PN database at [hashpn.space](https://hashpn.space/) (Parker, Bojičić & Frew 2016, J. Phys. Conf. Ser. 728, 032008).
- Planetary nebulae's `haflux`: integrated Hα fluxes from Frew, Bojičić & Parker (2013, MNRAS 431, 2; [CDS J/MNRAS/431/2](https://cdsarc.cds.unistra.fr/viz-bin/cat/J/MNRAS/431/2)).
- Comet magnitudes: observations from [COBS](https://cobs.si/), the Comet Observation database (Črni Vrh Observatory).
- `Sharpless.yaml`: Sharpless (1959), ApJS 4, 257, from CDS/VizieR catalog [VII/20](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/20); common names from Wikipedia's [Sharpless catalog](https://en.wikipedia.org/wiki/Sharpless_catalog) (CC BY-SA 4.0); positions, types and magnitudes from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `Caldwell.yaml`: Moore (1995), *The Caldwell Catalogue*, Sky & Telescope 90, 38; positions, sizes, types and magnitudes from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm, and for C 24 and C 35 from OpenNGC (CC BY-SA 4.0); designations and common names from Wikipedia's [Caldwell catalogue](https://en.wikipedia.org/wiki/Caldwell_catalogue) (CC BY-SA 4.0).
- `RCW.yaml`: Rodgers, Campbell & Whiteoak (1960), MNRAS 121, 103, from CDS/VizieR catalog [VII/216](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/216); positions, types and magnitudes from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `SNR.yaml`: Green (2025), J. Astrophys. Astr. 46, 14, *A Catalogue of Galactic Supernova Remnants* (2024 October version), from CDS/VizieR catalog [VII/297](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/297) and the optical notes of its [detailed listings](https://www.mrao.cam.ac.uk/surveys/snrs/); magnitudes and common names from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `Barnard.yaml`: Barnard (1927), *A Photographic Atlas of Selected Regions of the Milky Way*, from CDS/VizieR catalog [VII/220A](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/220A); positions, sizes and common names from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `vdB.yaml`: van den Bergh (1966), AJ 71, 990, from CDS/VizieR catalog [VII/21](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/21); positions, sizes and common names from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `vdBH.yaml`: van den Bergh & Herbst (1975), AJ 80, 208, from CDS/VizieR catalog [VII/218](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/218).
- `Arp.yaml`: Arp (1966), ApJS 14, 1, *Atlas of Peculiar Galaxies*, in Webb's *Contemporary Index* (1996), CDS/VizieR catalog [VII/192](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/192); positions, sizes, magnitudes and common names from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm; minor axes from OpenNGC.
- `HCG.yaml`: Hickson (1982), ApJ 255, 382, from CDS/VizieR catalog [VII/213](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/213); positions and common names from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `MCRN.yaml`: Magakian (2003), A&A 399, 141, *Merged catalogue of reflection nebulae*, from CDS/VizieR catalog [J/A+A/399/141](https://cdsarc.cds.unistra.fr/viz-bin/cat/J/A+A/399/141); sizes, positions and common names from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `Henize.yaml`: Henize (1956), ApJS 2, 315, identifiers and positions from [SIMBAD](https://simbad.cds.unistra.fr/) (Wenger et al. 2000, A&AS 143, 9); sizes from DEM.
- `DEM.yaml`: Davies, Elliott & Meaburn (1976), Mem. RAS 81, 89, from CDS/VizieR catalog [VII/232](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/232).
- `Harris.yaml`: Harris (1996), AJ 112, 1487, *Catalog of Parameters for Milky Way Globular Clusters* (2010 edition), from [physics.mcmaster.ca/~harris/mwgc.dat](https://physics.mcmaster.ca/~harris/mwgc.dat), supplied free of charge; sizes and common names from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `Dias.yaml`: Dias et al. (2002), A&A 389, 871, *New catalogue of optically visible open clusters and candidates* (version 3.5, 2016), from CDS/VizieR catalog [B/ocl](https://cdsarc.cds.unistra.fr/viz-bin/cat/B/ocl); magnitudes and common names from Gary Imm's Deep Sky Compendium (2026), data © Gary Imm.
- `DCld.yaml`: Hartley, Manchester, Smith, Tritton & Goss (1986), A&AS 63, 27, *A catalogue of southern dark clouds*, from CDS/VizieR catalog [VII/191](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/191).
- `AM.yaml`: Arp & Madore (1987), *A Catalogue of Southern Peculiar Galaxies and Associations*, Cambridge University Press, from CDS/VizieR catalog [VII/170](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/170).
- `LBN.yaml` minor axes and types: Lynds (1965), ApJS 12, 163, from CDS/VizieR catalog [VII/9](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/9).
- `LDN.yaml` sizes: Lynds (1962), ApJS 7, 1, from CDS/VizieR catalog [VII/7A](https://cdsarc.cds.unistra.fr/viz-bin/cat/VII/7A).
- Comet orbital elements: [IAU Minor Planet Center](https://www.minorplanetcenter.net/).
- Scattered moonlight: Winkler (2022), MNRAS 514, 208, *A revised simplified scattering model for the moonlit sky brightness profile based on photometry at SAAO*.
- Emission-line ratios by type: Madsen, Reynolds & Haffner (2006), ApJ 652, 401 (H II regions); Riesgo & López (2006), RMxAA (planetary nebulae); Fesen, Blair & Kirshner (1985), ApJ 292, 29 (supernova remnants); Mesa-Delgado et al. (2014), ApJ 785, 100 (NGC 6888).
- Narrowband filters in fast beams: [Chroma Technology](https://www.chroma.com/resources/technical-library/narrowband-astronomy-filter-f-number-data/)'s measured transmission curves at f/2 to f/5, which `scripts/filter_shift.py` fits.
- The IMX678 colour sensor's red, green and blue response: ZWO's ASI678MC manual (V1.0, §3.3).
- Solar-cycle airglow: Noll et al. (2012), A&A 543, A92, *An atmospheric radiation model for Cerro Paranal*; the monthly and 30-day 10.7 cm solar flux from NOAA's [Space Weather Prediction Center](https://www.swpc.noaa.gov/).
- Sky brightness tables: the zodiacal light (Table 17) and Pioneer 10's background starlight (Tables 35 and 36, from Toller 1981) of Leinert et al. (1998), A&AS 127, 1, *The 1997 reference of diffuse night sky brightness*.
- Mosaic grids and sky images for results, ideas from [Skywarden](https://skywarden.app/) (formerly Astro Planner) by Frank Adler. The browser's thumbnails come from the CDS [hips2fits](https://alasky.cds.unistra.fr/hips-image-services/hips2fits) service and its links open [Aladin Lite](https://aladin.cds.unistra.fr/AladinLite/) (Bonnarel et al. 2000, A&AS 143, 33; Boch & Fernique 2014, ASPC 485, 277), showing the Digitized Sky Survey (DSS2), based on photographic data obtained using the Oschin Schmidt Telescope and the UK Schmidt Telescope.
- Constellation boundaries and lookup from [astrogo](https://github.com/TuSKan/astrogo) (MIT), transcribing the IAU boundaries (Delporte 1930) from CDS/VizieR catalog [VI/49](https://vizier.cfa.harvard.edu/viz-bin/VizieR?-source=VI/49) (Davenhall & Leggett 1989).
- Weather and aerosols: [Open-Meteo](https://open-meteo.com/) (CC BY 4.0; aerosol data from the Copernicus Atmosphere Monitoring Service) and [7Timer!](https://www.7timer.info/).
- Seeing calibration: DIMM seeing from ESO's public [ambient conditions archive](https://archive.eso.org/cms/eso-data/ambient-conditions.html) for Paranal and La Silla, and Open-Meteo's archived ECMWF IFS forecasts (`scripts/calibrate_seeing.py`; the data is not redistributed, only two fitted constants).
- Emission-line calibration and the large emission nebulae's Hα fluxes: [Finkbeiner (2003)](https://doi.org/10.1086/374411)'s full-sky Hα map, as [LAMBDA](https://lambda.gsfc.nasa.gov/product/foreground/fg_halpha_get.html) serves it (`scripts/calibrate_lines.py` keeps one fitted constant; `scripts/hamap.py` writes the nebulae's `haflux` into the lists).
- Terrain horizon (`scripts/terrain2horizon.py`): [Terrain Tiles](https://registry.opendata.aws/terrain-tiles/) on AWS Open Data by Mapzen, from SRTM, GMTED2010, ETOPO1, NED and other national elevation models ([sources and attribution](https://github.com/tilezen/joerd/blob/master/docs/attribution.md)); buildings (and with `--no-canopy` trees and woods) © OpenStreetMap contributors (ODbL) through the [Overpass API](https://overpass-api.de/); trees from the [High Resolution Canopy Height Maps](https://registry.opendata.aws/dataforgood-fb-forests/) by WRI and Meta (CC BY 4.0; Tolan et al. 2024, [Remote Sensing of Environment 300, 113888](https://doi.org/10.1016/j.rse.2023.113888)).
- Geocoding: [Nominatim](https://nominatim.openstreetmap.org/), data © OpenStreetMap contributors (ODbL).
